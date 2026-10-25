package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrettyName(t *testing.T) {
	tests := []struct {
		data string
		want string
	}{
		{data: "NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n", want: "Ubuntu 24.04.1 LTS"},
		{data: "# comment\nPRETTY_NAME='Fedora Linux 41 (Workstation Edition)'\n", want: "Fedora Linux 41 (Workstation Edition)"},
		{data: "PRETTY_NAME=Arch\\ Linux\n", want: "Arch Linux"},
		{data: "PRETTY_NAME=\"Debian \\\"bookworm\\\"\"\n", want: "Debian \"bookworm\""},
		{data: "NAME=Gentoo\n", want: ""},
		{data: "", want: ""},
	}
	for _, tt := range tests {
		if got := prettyName([]byte(tt.data)); got != tt.want {
			t.Errorf("prettyName(%q) = %q, want %q", tt.data, got, tt.want)
		}
	}
}

func TestOSVersionOnLinux(t *testing.T) {
	dir := t.TempDir()
	etc := filepath.Join(dir, "etc-os-release")
	lib := filepath.Join(dir, "lib-os-release")
	if err := os.WriteFile(lib, []byte("PRETTY_NAME=\"Linux Mint 22\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := osReleasePaths
	osReleasePaths = []string{etc, lib}
	t.Cleanup(func() { osReleasePaths = orig })

	// /etc/os-release is missing: /usr/lib/os-release is read.
	if got := osVersion(); got != "Linux Mint 22" {
		t.Errorf("osVersion() = %q, want Linux Mint 22", got)
	}
	if err := os.WriteFile(etc, []byte("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := osVersion(); got != "Ubuntu 24.04.1 LTS" {
		t.Errorf("osVersion() = %q, want Ubuntu 24.04.1 LTS", got)
	}
	osReleasePaths = []string{filepath.Join(dir, "missing")}
	if got := osVersion(); got != "" {
		t.Errorf("osVersion() without os-release = %q, want \"\"", got)
	}
}
