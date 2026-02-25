package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

// i18nFS embeds all YAML translation files from the i18n subdirectory.
// To add a new language, drop a <lang>.yaml file in server/i18n/ and rebuild.
//
//go:embed i18n/*.yaml
var i18nFS embed.FS

// translations holds the loaded translation maps, keyed by language code (e.g. "en", "es").
var translations map[string]map[string]string

//nolint:gochecknoinits
func init() {
	translations = make(map[string]map[string]string)

	entries, err := fs.ReadDir(i18nFS, "i18n")
	if err != nil {
		// This should never happen since the embed path is static.
		panic(fmt.Sprintf("i18n: failed to read embedded i18n dir: %v", err))
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		lang := strings.TrimSuffix(entry.Name(), ".yaml")
		data, err := i18nFS.ReadFile("i18n/" + entry.Name())
		if err != nil {
			slog.Warn("i18n: failed to read translation file", "file", entry.Name(), "err", err)
			continue
		}

		var m map[string]string
		if err := yaml.Unmarshal(data, &m); err != nil {
			slog.Warn("i18n: failed to parse translation file", "file", entry.Name(), "err", err)
			continue
		}

		translations[lang] = m
	}

	// Ensure English baseline always exists.
	if _, ok := translations["en"]; !ok {
		panic("i18n: required English translation file (en.yaml) is missing or invalid")
	}
}

// GetTranslations returns the translation map for the requested language.
// It accepts full Accept-Language header values (e.g. "es-ES,es;q=0.9,en;q=0.8")
// and falls back to English if the language is not available.
func GetTranslations(acceptLang string) map[string]string {
	// Iterate through the comma-separated preference list.
	for _, part := range strings.Split(acceptLang, ",") {
		// Strip quality value: "es-ES;q=0.9" -> "es-ES"
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		// Normalize to lowercase base language: "es-ES" -> "es"
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

// SupportedLanguages returns the list of language codes currently loaded.
func SupportedLanguages() []string {
	langs := make([]string, 0, len(translations))
	for k := range translations {
		langs = append(langs, k)
	}
	return langs
}
