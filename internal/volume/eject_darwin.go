//go:build darwin

package volume

import (
	"fmt"
	"os/exec"
)

// runDiskutil runs diskutil with args and returns its combined output. It is
// a variable so tests can substitute a fake implementation.
var runDiskutil = func(args ...string) ([]byte, error) {
	return exec.Command("diskutil", args...).CombinedOutput()
}

// Eject ejects the volume mounted at mountPoint via `diskutil eject`. On
// failure, the command's output is included in the returned error.
func Eject(mountPoint string) error {
	out, err := runDiskutil("eject", mountPoint)
	if err != nil {
		return fmt.Errorf("diskutil eject %s: %w: %s", mountPoint, err, out)
	}
	return nil
}
