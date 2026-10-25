package gui

import (
	"os/exec"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// swVers runs `sw_vers -productVersion`; the seam the tests replace.
var swVers = func() ([]byte, error) {
	return exec.Command("sw_vers", "-productVersion").Output()
}

// osVersion is the macOS version, such as "15.1.1", or "" if unknown.
func osVersion() string {
	out, err := swVers()
	if err != nil {
		logx.Warn("gui: cannot read the macOS version", "err", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
