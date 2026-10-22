// Package i18n resolves the user's preferred language and translates
// message keys using the JSON locale catalogs embedded in the frontend
// package. The same catalogs back the Svelte UI, so Go and the frontend
// always agree on which keys and placeholders exist.
package i18n

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/frontend"
)

// Supported lists every language with a locale catalog, in the order
// language pickers should offer them.
var Supported = []string{"en", "ru", "de", "es", "fr", "pt-BR", "zh-Hans", "ja"}

// Resolve picks the language to use. An explicit preference that matches a
// supported language wins outright. Otherwise the first system language
// (most preferred first) that maps to a supported language is used.
// Otherwise English.
func Resolve(preference string, system []string) string {
	if preference != "" {
		if tag, ok := supportedTag(preference); ok {
			return tag
		}
	}
	for _, sys := range system {
		if tag, ok := matchSystemTag(sys); ok {
			return tag
		}
	}
	return "en"
}

// matchSystemTag maps one system language tag (e.g. "ru-RU", "zh-Hans-CN",
// "pt-PT") to a supported catalog language, if any.
func matchSystemTag(sys string) (string, bool) {
	sys = strings.TrimSpace(sys)
	if sys == "" {
		return "", false
	}
	switch {
	case hasPrefixFold(sys, "zh-Hans"):
		return "zh-Hans", true
	case hasPrefixFold(sys, "pt"):
		return "pt-BR", true
	default:
		base := sys
		if i := strings.IndexByte(sys, '-'); i >= 0 {
			base = sys[:i]
		}
		return supportedTag(base)
	}
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// supportedTag reports whether tag matches a supported language
// case-insensitively, returning the canonically-cased supported tag.
func supportedTag(tag string) (string, bool) {
	for _, s := range Supported {
		if strings.EqualFold(s, tag) {
			return s, true
		}
	}
	return "", false
}

// Catalog holds one language's translated messages plus an English
// fallback for keys it is missing.
type Catalog struct {
	lang     string
	messages map[string]string
	fallback map[string]string
}

// newCatalog builds a Catalog directly from message maps, bypassing
// embedded-file loading. It exists so tests can exercise fallback and
// missing-key behavior without depending on the full locale set.
func newCatalog(lang string, msgs, fallback map[string]string) *Catalog {
	return &Catalog{lang: lang, messages: msgs, fallback: fallback}
}

// Load reads the locale catalog for lang from the embedded frontend
// locales. For any language other than English, the English catalog is
// loaded too and used as the fallback for keys lang is missing.
func Load(lang string) (*Catalog, error) {
	messages, err := loadLocale(lang)
	if err != nil {
		return nil, err
	}

	fallback := messages
	if lang != "en" {
		fallback, err = loadLocale("en")
		if err != nil {
			return nil, err
		}
	}
	return newCatalog(lang, messages, fallback), nil
}

func loadLocale(lang string) (map[string]string, error) {
	data, err := frontend.Locales.ReadFile("locales/" + lang + ".json")
	if err != nil {
		return nil, fmt.Errorf("i18n: load locale %q: %w", lang, err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("i18n: parse locale %q: %w", lang, err)
	}
	return messages, nil
}

// Lang returns the language this catalog was loaded for.
func (c *Catalog) Lang() string {
	return c.lang
}

// T translates key, substituting "{name}" placeholders with the matching
// entries from params. A key missing from this catalog falls back to the
// English catalog, then to the key itself.
func (c *Catalog) T(key string, params map[string]string) string {
	text, ok := c.messages[key]
	if !ok {
		text, ok = c.fallback[key]
		if !ok {
			return key
		}
	}
	return substitute(text, params)
}

func substitute(text string, params map[string]string) string {
	for name, value := range params {
		text = strings.ReplaceAll(text, "{"+name+"}", value)
	}
	return text
}
