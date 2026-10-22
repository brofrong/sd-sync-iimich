package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// frontendSrc is the Svelte source tree, relative to this package.
const frontendSrc = "../../frontend/src"

// literalTCall matches t("key") and t('key') calls with a literal key.
// Dynamic keys (template literals, variables) are not matched; they must
// be built from keys the catalogs already have.
var literalTCall = regexp.MustCompile(`\bt\(\s*(?:"([^"]+)"|'([^']+)')`)

// TestFrontendLocaleKeysUsedExist checks that every literal key the
// frontend translates exists in the English catalog, so a typo or a key
// added to the code but not to the catalogs fails here instead of showing
// the raw key in the window.
func TestFrontendLocaleKeysUsedExist(t *testing.T) {
	en, err := loadLocale("en")
	if err != nil {
		t.Fatalf("loadLocale(en) error = %v", err)
	}

	used := map[string][]string{} // key -> files using it
	err = filepath.WalkDir(frontendSrc, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".svelte" && ext != ".ts" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(frontendSrc, path)
		for _, m := range literalTCall.FindAllStringSubmatch(string(data), -1) {
			key := m[1] + m[2]
			if files := used[key]; len(files) == 0 || files[len(files)-1] != rel {
				used[key] = append(files, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", frontendSrc, err)
	}
	if len(used) == 0 {
		t.Fatalf("found no literal t(\"…\") calls under %s; is the scanner still matching the frontend's i18n calls?", frontendSrc)
	}

	keys := make([]string, 0, len(used))
	for key := range used {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := en[key]; !ok {
			t.Errorf("key %q used in %s is missing from locales/en.json", key, strings.Join(used[key], ", "))
		}
	}
}
