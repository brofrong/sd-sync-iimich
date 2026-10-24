package i18n

import "testing"

// TestSystemLanguagesOnWindows reads the real (CI runner's) display
// languages: there is always at least one, as a BCP-47 tag.
func TestSystemLanguagesOnWindows(t *testing.T) {
	got := SystemLanguages()
	if len(got) == 0 {
		t.Fatal("SystemLanguages() = [], want the user's display languages")
	}
	for _, tag := range got {
		if tag == "" {
			t.Fatalf("SystemLanguages() = %q, has an empty tag", got)
		}
	}
}
