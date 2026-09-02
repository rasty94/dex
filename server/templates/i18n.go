package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

// i18nFS embeds the YAML translation files. To add a language, drop a
// <lang>.yaml in server/templates/i18n/ and rebuild — no Go changes needed.
//
//go:embed i18n/*.yaml
var i18nFS embed.FS

// translations holds the loaded translation maps, keyed by language code.
var translations map[string]map[string]string

// is nothing to configure and no order to depend on.
//
//nolint:gochecknoinits // the embedded files are fixed at build time, so there
func init() {
	translations = make(map[string]map[string]string)

	entries, err := fs.ReadDir(i18nFS, "i18n")
	if err != nil {
		// Cannot happen: the embed path is static and checked at build time.
		panic(fmt.Sprintf("i18n: failed to read the embedded i18n dir: %v", err))
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		lang := strings.TrimSuffix(entry.Name(), ".yaml")
		data, err := i18nFS.ReadFile("i18n/" + entry.Name())
		if err != nil {
			slog.Warn("i18n: failed to read a translation file", "file", entry.Name(), "err", err)
			continue
		}

		var m map[string]string
		if err := yaml.Unmarshal(data, &m); err != nil {
			slog.Warn("i18n: failed to parse a translation file", "file", entry.Name(), "err", err)
			continue
		}

		translations[lang] = m
	}

	// Every other language falls back to English key by key, so without it the
	// pages would render blanks rather than the wrong language.
	en, ok := translations["en"]
	if !ok {
		panic("i18n: the English translation file (en.yaml) is missing or invalid")
	}

	// Merge each language over English once, here, rather than on every render.
	// A file that is missing a key then renders the English string for it: a page
	// half in English still works, and a page with blank labels does not.
	for lang, tr := range translations {
		if lang == "en" {
			continue
		}
		merged := make(map[string]string, len(en))
		for k, v := range en {
			merged[k] = v
		}
		for k, v := range tr {
			merged[k] = v
		}
		translations[lang] = merged
	}
}

// GetTranslations returns the translation map for an Accept-Language header
// value, e.g. "es-ES,es;q=0.9,en;q=0.8". Unknown languages fall back to English.
// The returned map is shared and must not be modified.
func GetTranslations(acceptLang string) map[string]string {
	for _, part := range strings.Split(acceptLang, ",") {
		// Strip the quality value: "es-ES;q=0.9" -> "es-ES".
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		// Normalise to the base language: "es-ES" -> "es".
		lang := strings.ToLower(tag)
		if idx := strings.IndexByte(lang, '-'); idx != -1 {
			lang = lang[:idx]
		}
		if tr, ok := translations[lang]; ok {
			return tr
		}
	}
	return translations["en"]
}
