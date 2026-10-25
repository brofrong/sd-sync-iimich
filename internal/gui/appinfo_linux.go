package gui

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// osReleasePaths are where os-release(5) may be, in the order it says to
// look; the tests replace them.
var osReleasePaths = []string{"/etc/os-release", "/usr/lib/os-release"}

// osVersion is the distribution's PRETTY_NAME from os-release, such as
// "Ubuntu 24.04.1 LTS", or "" if unknown.
func osVersion() string {
	for _, path := range osReleasePaths {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			logx.Warn("gui: cannot read os-release", "path", path, "err", err)
			return ""
		}
		return prettyName(data)
	}
	return ""
}

// prettyName is the PRETTY_NAME value of the os-release data, unquoted as
// a shell would (os-release(5)): "…" with backslash escapes, '…' as is, or
// a bare value with backslash escapes. "" when there is none.
func prettyName(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		value, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "PRETTY_NAME=")
		if !ok {
			continue
		}
		switch {
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			return value[1 : len(value)-1]
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			return unescape(value[1 : len(value)-1])
		default:
			return unescape(value)
		}
	}
	return ""
}

// unescape drops the backslash before each escaped character.
func unescape(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}
