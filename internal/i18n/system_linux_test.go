package i18n

import (
	"reflect"
	"testing"
)

// setLocaleEnv sets the locale variables SystemLanguages reads, clearing
// the ones not given.
func setLocaleEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, env[k])
	}
}

func TestSystemLanguagesFromLang(t *testing.T) {
	setLocaleEnv(t, map[string]string{"LANG": "de_DE.UTF-8"})

	got := SystemLanguages()
	if want := []string{"de-DE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SystemLanguages() = %v, want %v", got, want)
	}
	if lang := Resolve("", got); lang != "de" {
		t.Fatalf("Resolve(\"\", %v) = %q, want \"de\"", got, lang)
	}
}

func TestSystemLanguagesPrecedence(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"LC_ALL overrides LC_MESSAGES and LANG", map[string]string{"LC_ALL": "fr_FR.UTF-8", "LC_MESSAGES": "de_DE.UTF-8", "LANG": "es_ES.UTF-8"}, []string{"fr-FR"}},
		{"LC_MESSAGES overrides LANG", map[string]string{"LC_MESSAGES": "ru_RU.UTF-8", "LANG": "en_US.UTF-8"}, []string{"ru-RU"}},
		{"LANGUAGE lists preferences before the locale", map[string]string{"LANGUAGE": "pt_BR:ja", "LANG": "en_US.UTF-8"}, []string{"pt-BR", "ja", "en-US"}},
		{"LANGUAGE is ignored for the C locale", map[string]string{"LANGUAGE": "de", "LANG": "C"}, nil},
		{"modifier and codeset are dropped", map[string]string{"LANG": "ca_ES.UTF-8@valencia"}, []string{"ca-ES"}},
		{"POSIX locale has no language", map[string]string{"LANG": "POSIX"}, nil},
		{"nothing set", map[string]string{}, nil},
		{"duplicates are dropped", map[string]string{"LANGUAGE": "ru:ru_RU", "LANG": "ru_RU.UTF-8"}, []string{"ru", "ru-RU"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLocaleEnv(t, tt.env)
			if got := SystemLanguages(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SystemLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}
