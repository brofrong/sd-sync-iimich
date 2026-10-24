package i18n

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		preference string
		system     []string
		want       string
	}{
		{"system russian preferred over english", "", []string{"ru-RU", "en"}, "ru"},
		{"chinese simplified region variant", "", []string{"zh-Hans-CN"}, "zh-Hans"},
		// Windows and Linux name Chinese by region, without a script.
		{"chinese china region means simplified", "", []string{"zh-CN"}, "zh-Hans"},
		{"chinese singapore region means simplified", "", []string{"zh-SG"}, "zh-Hans"},
		{"bare chinese means simplified", "", []string{"zh"}, "zh-Hans"},
		{"chinese taiwan region is traditional", "", []string{"zh-TW"}, "en"},
		{"chinese hong kong region is traditional", "", []string{"zh-HK"}, "en"},
		{"chinese traditional script", "", []string{"zh-Hant-TW"}, "en"},
		{"portuguese portugal maps to brazilian catalog", "", []string{"pt-PT"}, "pt-BR"},
		{"portuguese brazil matches directly", "", []string{"pt-BR"}, "pt-BR"},
		{"unsupported system language falls back to english", "", []string{"ko-KR"}, "en"},
		{"explicit preference wins over system", "de", []string{"ru"}, "de"},
		{"unsupported preference falls back to system", "xx", []string{"ru"}, "ru"},
		{"no preference and no system languages", "", nil, "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.preference, tt.system); got != tt.want {
				t.Errorf("Resolve(%q, %v) = %q, want %q", tt.preference, tt.system, got, tt.want)
			}
		})
	}
}

func TestTSubstitutesParams(t *testing.T) {
	cat, err := Load("en")
	if err != nil {
		t.Fatalf("Load(en) error = %v", err)
	}

	title := cat.T("status.server_unreachable.title", nil)
	if title == "" || title == "status.server_unreachable.title" {
		t.Fatalf("T(status.server_unreachable.title) = %q, want a translated, non-empty string", title)
	}

	detail := cat.T("status.server_unreachable.detail", map[string]string{
		"host":   "immich.example.com",
		"detail": "timeout",
	})
	if !strings.Contains(detail, "immich.example.com") {
		t.Errorf("T(status.server_unreachable.detail, ...) = %q, want it to contain the substituted host", detail)
	}
	if strings.Contains(detail, "{host}") {
		t.Errorf("T(status.server_unreachable.detail, ...) = %q, want no raw {host} placeholder left", detail)
	}
}

func TestTFallsBackToEnglishThenKey(t *testing.T) {
	fallback := map[string]string{"greeting.hello": "Hello"}
	cat := newCatalog("xx", map[string]string{}, fallback)

	if got := cat.T("greeting.hello", nil); got != "Hello" {
		t.Errorf("T(greeting.hello) = %q, want fallback English text %q", got, "Hello")
	}
	if got := cat.T("does.not.exist", nil); got != "does.not.exist" {
		t.Errorf("T(does.not.exist) = %q, want the key itself", got)
	}
}

func TestLocalesHaveAllEnglishKeys(t *testing.T) {
	en, err := loadLocale("en")
	if err != nil {
		t.Fatalf("loadLocale(en) error = %v", err)
	}

	for _, lang := range Supported {
		msgs, err := loadLocale(lang)
		if err != nil {
			t.Fatalf("loadLocale(%q) error = %v", lang, err)
		}
		for key := range en {
			value, ok := msgs[key]
			if !ok {
				t.Errorf("locale %q is missing key %q", lang, key)
				continue
			}
			if strings.TrimSpace(value) == "" {
				t.Errorf("locale %q has an empty value for key %q", lang, key)
			}
		}
	}
}

var placeholderPattern = regexp.MustCompile(`\{[a-zA-Z_]+\}`)

func placeholderSet(s string) map[string]struct{} {
	matches := placeholderPattern.FindAllString(s, -1)
	set := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		set[m] = struct{}{}
	}
	return set
}

func TestLocalesKeepPlaceholders(t *testing.T) {
	en, err := loadLocale("en")
	if err != nil {
		t.Fatalf("loadLocale(en) error = %v", err)
	}

	for _, lang := range Supported {
		if lang == "en" {
			continue
		}
		msgs, err := loadLocale(lang)
		if err != nil {
			t.Fatalf("loadLocale(%q) error = %v", lang, err)
		}
		for key, enText := range en {
			want := placeholderSet(enText)
			got := placeholderSet(msgs[key])
			if !reflect.DeepEqual(want, got) {
				t.Errorf("locale %q key %q placeholders = %v, want %v", lang, key, got, want)
			}
		}
	}
}
