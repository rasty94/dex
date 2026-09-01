package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
