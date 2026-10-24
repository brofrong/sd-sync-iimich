package i18n

import (
	"os"
	"slices"
	"strings"
)

// SystemLanguages returns the user's language preferences from the locale
// environment, most preferred first, as BCP-47 tags ("de_DE.UTF-8" →
// "de-DE"), or nil without any. The locale is the first of LC_ALL,
// LC_MESSAGES and LANG that is set; GNU gettext's LANGUAGE, a
// colon-separated list, comes before it, except for the C/POSIX locale,
// which turns translations off.
func SystemLanguages() []string {
	locale := ""
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			locale = v
			break
		}
	}
	if localeTag(locale) == "" {
		return nil
	}

	var langs []string
	add := func(locale string) {
		if tag := localeTag(locale); tag != "" && !slices.Contains(langs, tag) {
			langs = append(langs, tag)
		}
	}
	for _, l := range strings.Split(os.Getenv("LANGUAGE"), ":") {
		add(l)
	}
	add(locale)
	return langs
}

// localeTag turns a POSIX locale name, language[_territory][.codeset][@modifier],
// into a BCP-47 tag: "pt_BR.UTF-8" → "pt-BR". It returns "" for the C and
// POSIX locales, which name no language.
func localeTag(locale string) string {
	if i := strings.IndexAny(locale, ".@"); i >= 0 {
		locale = locale[:i]
	}
	locale = strings.TrimSpace(locale)
	if locale == "" || locale == "C" || locale == "POSIX" {
		return ""
	}
	return strings.ReplaceAll(locale, "_", "-")
}
