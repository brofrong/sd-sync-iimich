//go:build !windows

package desktop

import "os/exec"

// runCommand runs a command and waits for it.
func runCommand(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// run runs the desktop's helper commands (open, xdg-open); tests replace it.
var run = runCommand
