package gui

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// rtlGetVersion is windows.RtlGetVersion (the real version, whatever the
// manifest says); the seam the tests replace.
var rtlGetVersion = windows.RtlGetVersion

// verNTWorkstation is OsVersionInfoEx.ProductType for a desktop Windows
// (VER_NT_WORKSTATION), as opposed to a server.
const verNTWorkstation = 1

// osVersion is the Windows version and build, such as "11 (10.0.22631)":
// Windows 11 still reports version 10.0, told apart by its build number
// (22000 and later). A server, or an older Windows, gets the numbers only.
func osVersion() string {
	v := rtlGetVersion()
	numbers := fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	if v.MajorVersion != 10 || v.MinorVersion != 0 || v.ProductType != verNTWorkstation {
		return numbers
	}
	name := "10"
	if v.BuildNumber >= 22000 {
		name = "11"
	}
	return name + " (" + numbers + ")"
}
