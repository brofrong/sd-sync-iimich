package version

import "testing"

func TestIsRelease(t *testing.T) {
	original := Current
	t.Cleanup(func() { Current = original })

	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"dev", false},
		{"", false},
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v1.2", false},
	} {
		Current = tt.value
		if got := IsRelease(); got != tt.want {
			t.Errorf("IsRelease() with Current=%q = %v, want %v", tt.value, got, tt.want)
		}
	}
}
