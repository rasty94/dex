package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/dexidp/dex/server"
)

// unchangedMarker replaces a secret when a connector's config is rendered for
// editing, and means "leave this as it was" when the form comes back.
//
// This is not a security boundary: an administrator who can edit connectors can
// generally read the config some other way. It keeps bind passwords and client
// secrets out of a browser window, a screenshot and the back button, which is
// where they leak by accident rather than by attack.
const unchangedMarker = "__unchanged__"

// secretKeyParts decide which fields get hidden.
//
// ponytail: a heuristic on key names, not a schema. It errs toward hiding, and
// a field it does not recognize is shown in full — so when adding a connector
// type with an unusual secret field name, add it here.
var secretKeyParts = []string{
	"secret", "password", "passwd", "token", "credential", "privatekey", "apikey",
}

func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	if k == "bindpw" || strings.HasSuffix(k, "pw") {
		return true
	}
	for _, part := range secretKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// redactSecrets returns config with every secret-looking string replaced by the
// marker, formatted for a textarea.
func redactSecrets(config []byte) (string, error) {
	if len(config) == 0 {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(config, &v); err != nil {
		// Not valid JSON: show it as stored rather than hiding the problem, so
		// whoever has to fix it can see what is actually there.
		return string(config), nil
	}
	return indent(walkRedact(v))
}

func walkRedact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if s, ok := val.(string); ok && s != "" && isSecretKey(k) {
				out[k] = unchangedMarker
				continue
			}
			out[k] = walkRedact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = walkRedact(val)
		}
		return out
	}
	return v
}

// restoreSecrets puts back every value the operator left as the marker, taking
// it from the config already stored. A field they actually typed into is used
// as given, so rotating a secret works.
func restoreSecrets(submitted, stored []byte) ([]byte, error) {
	var sub any
	if err := json.Unmarshal(submitted, &sub); err != nil {
		return nil, fmt.Errorf("the configuration is not valid JSON: %w", err)
	}

	var old any
	if len(stored) > 0 {
		// A stored config that does not parse leaves nothing to restore from;
		// the marker then fails validation below, which is the honest outcome.
		_ = json.Unmarshal(stored, &old)
	}

	merged := walkRestore(sub, old)
	return json.Marshal(merged)
}

func walkRestore(submitted, stored any) any {
	switch t := submitted.(type) {
	case map[string]any:
		storedMap, _ := stored.(map[string]any)
		out := make(map[string]any, len(t))
		for k, val := range t {
			var prev any
			if storedMap != nil {
				prev = storedMap[k]
			}
			if s, ok := val.(string); ok && s == unchangedMarker {
				if prev != nil {
					out[k] = prev
				}
				// A marker with nothing behind it is dropped: writing the literal
				// string as a password would be worse than leaving it unset.
				continue
			}
			out[k] = walkRestore(val, prev)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = walkRestore(val, nil)
		}
		return out
	}
	return submitted
}

// validateConnectorConfig checks a config against the connector type before it
// reaches dex. dex only checks that the JSON parses, so a typo like "clientId"
// for "clientID" is stored happily and then breaks every login through that
// connector. Decoding into the real config struct with unknown fields rejected
// catches exactly that class of mistake.
func validateConnectorConfig(connType string, config []byte) error {
	factory, ok := server.ConnectorsConfig[connType]
	if !ok {
		return fmt.Errorf("unknown connector type %q. Known types: %s", connType, knownConnectorTypes())
	}

	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(factory()); err != nil {
		return fmt.Errorf("this is not a valid %s configuration: %w", connType, err)
	}
	return nil
}

func knownConnectorTypes() string {
	types := make([]string, 0, len(server.ConnectorsConfig))
	for t := range server.ConnectorsConfig {
		types = append(types, t)
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

// ConnectorTypes is the sorted list offered in the new-connector form.
func ConnectorTypes() []string {
	types := make([]string, 0, len(server.ConnectorsConfig))
	for t := range server.ConnectorsConfig {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

func indent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ConnectorSkeleton builds a starting configuration for a connector type by
// walking its real config struct. Writing this JSON by hand against a schema you
// cannot see is the most error-prone thing left in the panel; the validator
// already says when it is wrong, and this says what it should look like.
//
// Fields come out in struct order, not alphabetical. Connector configs are
// written with the essentials first — issuer, clientID, clientSecret — and
// sorting them scatters those among the tuning flags.
func ConnectorSkeleton(connType string) (string, error) {
	factory, ok := server.ConnectorsConfig[connType]
	if !ok {
		return "", fmt.Errorf("unknown connector type %q", connType)
	}
	b, err := json.MarshalIndent(skeletonFor(reflect.TypeOf(factory()), 0), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// maxSkeletonDepth stops a config type that refers to itself, and keeps deeply
// nested shapes from producing a wall of JSON nobody reads.
const maxSkeletonDepth = 3

// orderedFields marshals to a JSON object preserving insertion order, which a
// map cannot do.
type orderedFields []field

type field struct {
	name  string
	value any
}

func (o orderedFields) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(f.name)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func skeletonFor(t reflect.Type, depth int) any {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || depth > maxSkeletonDepth {
		return nil
	}

	//nolint:exhaustive // Kinds that cannot appear in a JSON config (channels,
	// funcs, complex numbers) fall through to nil on purpose.
	switch t.Kind() {
	case reflect.Struct:
		out := orderedFields{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, ok := jsonFieldName(f)
			if !ok {
				continue
			}
			// Embedded structs without a json name are inlined, the way the
			// decoder will read them.
			if f.Anonymous && name == "" {
				if inner, ok := skeletonFor(f.Type, depth).(orderedFields); ok {
					out = append(out, inner...)
				}
				continue
			}
			out = append(out, field{name: name, value: skeletonFor(f.Type, depth+1)})
		}
		return out
	case reflect.Slice, reflect.Array:
		return []any{}
	case reflect.Map:
		return map[string]any{}
	case reflect.String:
		return ""
	case reflect.Bool:
		return false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return 0
	case reflect.Float32, reflect.Float64:
		return 0.0
	}
	return nil
}

// jsonFieldName reads a struct tag, reporting the field's name and whether it
// is serialized at all.
func jsonFieldName(f reflect.StructField) (name string, ok bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ = strings.Cut(tag, ",")
	if name == "" && !f.Anonymous {
		name = f.Name
	}
	return name, true
}
