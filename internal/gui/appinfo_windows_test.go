package gui

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestOSVersionOnWindows(t *testing.T) {
	orig := rtlGetVersion
	t.Cleanup(func() { rtlGetVersion = orig })

	tests := []struct {
		info windows.OsVersionInfoEx
		want string
	}{
		{info: windows.OsVersionInfoEx{MajorVersion: 10, BuildNumber: 22631, ProductType: 1}, want: "11 (10.0.22631)"},
		{info: windows.OsVersionInfoEx{MajorVersion: 10, BuildNumber: 19045, ProductType: 1}, want: "10 (10.0.19045)"},
		// Windows Server reports 10.0 too; only the numbers are shown.
		{info: windows.OsVersionInfoEx{MajorVersion: 10, BuildNumber: 20348, ProductType: 3}, want: "10.0.20348"},
		{info: windows.OsVersionInfoEx{MajorVersion: 6, MinorVersion: 3, BuildNumber: 9600, ProductType: 1}, want: "6.3.9600"},
	}
	for _, tt := range tests {
		info := tt.info
		rtlGetVersion = func() *windows.OsVersionInfoEx { return &info }
		if got := osVersion(); got != tt.want {
			t.Errorf("osVersion() for %d.%d.%d = %q, want %q", info.MajorVersion, info.MinorVersion, info.BuildNumber, got, tt.want)
		}
	}
}
