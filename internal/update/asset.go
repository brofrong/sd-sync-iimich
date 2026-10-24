package update

// AssetName is the release file that updates the app on system goos (a
// GOOS value), "" when there is none. All of them are listed in the one
// checksums.txt of the release.
func AssetName(goos string) string {
	switch goos {
	case "darwin":
		// The bare executable: v0.3.0's updater downloads it by this name,
		// so it must never be renamed.
		return "sd-sync-immich"
	case "windows":
		return "SD-Sync-Immich-Setup-x64.exe"
	case "linux":
		return "SD-Sync-Immich-x86_64.AppImage"
	}
	return ""
}
