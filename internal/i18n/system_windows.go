package i18n

import "golang.org/x/sys/windows"

// SystemLanguages returns the user's Windows display languages, most
// preferred first, as BCP-47 tags (e.g. "ru-RU"), or nil if they can't be
// read.
func SystemLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return nil
	}
	return langs
}
