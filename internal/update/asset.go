package update

// AssetName is the release file that updates the app on system goos and
// architecture goarch (GOOS and GOARCH values), "" when there is none. All
// of them are listed in the one checksums.txt of the release.
//
// The Windows installer and the Linux AppImage are built for amd64 only: on
// any other architecture there is no update file, so a build for, say,
// aarch64 Linux is told there are no updates instead of replacing itself
// with an x86_64 AppImage that would not start. The macOS executable is
// universal (arm64 and amd64).
func AssetName(goos, goarch string) string {
	switch goos {
	case "darwin":
		// The bare executable: v0.3.0's updater downloads it by this name,
		// so it must never be renamed.
		return "sd-sync-immich"
	case "windows":
		if goarch == "amd64" {
			return "SD-Sync-Immich-Setup-x64.exe"
		}
	case "linux":
		if goarch == "amd64" {
			return "SD-Sync-Immich-x86_64.AppImage"
		}
	}
	return ""
}
