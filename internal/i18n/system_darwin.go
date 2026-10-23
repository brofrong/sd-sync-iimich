package i18n

import (
	"os/exec"
	"regexp"
)

// systemLanguagePattern matches each quoted language tag in the output of
// `defaults read -g AppleLanguages`, e.g.:
//
//	(
//	    "ru-RU",
//	    "en"
//	)
var systemLanguagePattern = regexp.MustCompile(`"([^"]+)"`)

// SystemLanguages returns the user's macOS language preferences, most
// preferred first, or nil if they can't be read.
func SystemLanguages() []string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return nil
	}

	matches := systemLanguagePattern.FindAllStringSubmatch(string(out), -1)
	if len(matches) == 0 {
		return nil
	}
	langs := make([]string, 0, len(matches))
	for _, m := range matches {
		langs = append(langs, m[1])
	}
	return langs
}
