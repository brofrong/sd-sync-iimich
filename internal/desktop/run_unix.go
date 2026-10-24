//go:build !windows

package desktop

import "os/exec"

// command is the helper command name with args, with the environment
// helperEnv gives it.
func command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = helperEnv()
	return cmd
}

// runCommand runs cmd and waits for it.
func runCommand(cmd *exec.Cmd) error { return cmd.Run() }

// run runs the desktop's helper commands (open, xdg-open); tests replace
// it, so nothing is started.
var run = runCommand
