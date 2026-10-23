package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		n    int
		want string
	}{
		{n: 2, want: "three\nfour"},
		{n: 4, want: "one\ntwo\nthree\nfour"},
		{n: 10, want: "one\ntwo\nthree\nfour"},
	}
	for _, tt := range tests {
		got, err := tailLines(path, tt.n)
		if err != nil {
			t.Fatalf("tailLines(%d): %v", tt.n, err)
		}
		if got != tt.want {
			t.Fatalf("tailLines(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTailLinesDropsPartialFirstLineOfLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	line := strings.Repeat("x", 99) + "\n"
	content := strings.Repeat(line, logTailBytes/len(line)+10) + "last\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := tailLines(path, 1<<30) // every line read
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if lines[len(lines)-1] != "last" {
		t.Fatalf("last line = %q, want %q", lines[len(lines)-1], "last")
	}
	for i, l := range lines[:len(lines)-1] {
		if len(l) != 99 {
			t.Fatalf("line %d has length %d, want 99 (partial line kept?)", i, len(l))
		}
	}
}

func TestTailLinesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := tailLines(path, 5)
	if err != nil || got != "" {
		t.Fatalf("tailLines(empty) = %q, %v; want \"\", nil", got, err)
	}
}
