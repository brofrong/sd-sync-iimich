package main

import (
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		args []string
		want command
	}{
		// Opened from Finder, a Start menu shortcut or an AppImage: run.
		{nil, command{name: "run"}},
		{[]string{}, command{name: "run"}},
		// Old macOS passes a process serial number.
		{[]string{"-psn_0_123456"}, command{name: "run"}},
		{[]string{"run"}, command{name: "run"}},
		// The autostart entries on Windows and Linux.
		{[]string{"run", "--autostart"}, command{name: "run", autostart: true}},
		{[]string{"scan-test", "/media/u/CARD"}, command{name: "scan-test", args: []string{"/media/u/CARD"}}},
		{[]string{"install"}, command{name: "install", args: []string{}}},
		// Unknown commands still print usage.
		{[]string{"bogus"}, command{name: "bogus", args: []string{}}},
		{[]string{"-psn_0_1", "extra"}, command{name: "-psn_0_1", args: []string{"extra"}}},
		{[]string{"--autostart"}, command{name: "--autostart", args: []string{}}},
	}
	for _, tt := range tests {
		got := parseCommand(tt.args)
		if tt.want.name == "run" {
			// run ignores anything but --autostart.
			got.args, tt.want.args = nil, nil
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseCommand(%q) = %+v, want %+v", tt.args, got, tt.want)
		}
	}
}
