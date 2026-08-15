//go:build darwin

package settings

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateTextPreservesUTF8(t *testing.T) {
	input := strings.Repeat("🙂", 10)
	got := truncateText(input, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText() returned invalid UTF-8: %q", got)
	}
	if got != strings.Repeat("🙂", 5)+"…" {
		t.Fatalf("truncateText() = %q", got)
	}
}
