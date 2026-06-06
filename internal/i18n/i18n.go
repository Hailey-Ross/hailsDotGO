package i18n

import (
	"embed"
	"encoding/json"
	"log"
)

//go:embed locales/*.json
var localeFS embed.FS

// Supported is the set of valid language codes.
var Supported = map[string]bool{
	"en": true,
	"es": true,
	"fr": true,
	"de": true,
}

type bundle map[string]string

var bundles = map[string]bundle{}

func init() {
	for lang := range Supported {
		data, err := localeFS.ReadFile("locales/" + lang + ".json")
		if err != nil {
			log.Fatalf("i18n: missing locale file for %q: %v", lang, err)
		}
		var b bundle
		if err := json.Unmarshal(data, &b); err != nil {
			log.Fatalf("i18n: parse %s.json: %v", lang, err)
		}
		bundles[lang] = b
	}
}

// TFunc returns a translation function for the given language code.
// Missing keys fall back to English; keys missing from English return the key itself.
func TFunc(lang string) func(string) string {
	b, ok := bundles[lang]
	if !ok {
		b = bundles["en"]
	}
	en := bundles["en"]
	return func(key string) string {
		if v, ok := b[key]; ok && v != "" {
			return v
		}
		if v, ok := en[key]; ok && v != "" {
			return v
		}
		return key
	}
}
