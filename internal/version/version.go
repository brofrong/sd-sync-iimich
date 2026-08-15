package version

import "regexp"

// Current is replaced with the release tag by GitHub Actions through -ldflags.
var Current = "dev"

var releasePattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

func IsRelease() bool {
	return releasePattern.MatchString(Current)
}
