#!/usr/bin/env bash
# Builds a release: the frontend, the arm64 and amd64 binaries, a
# universal binary, the ad-hoc signed "SD Sync Immich.app" and its zip,
# and checksums.txt, all into <outdir>:
#
#   sd-sync-immich                universal binary: v0.3.0's updater and the
#                                 in-app updater download this asset
#   sd-sync-immich-darwin-arm64   Apple Silicon only
#   sd-sync-immich-darwin-amd64   Intel only
#   SD Sync Immich.app            the bundle (not uploaded; zipped below)
#   SD-Sync-Immich.zip            the bundle for manual installation
#   checksums.txt                 SHA-256 of the four files above
#
# Usage: scripts/build-app.sh <version> <outdir>
# The version (e.g. v0.4.0) is embedded with -ldflags. Set WAILS3 to use an
# installed Wails CLI (see scripts/build-frontend.sh).
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <version> <outdir>" >&2
  exit 2
fi
version="$1"
root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$2"
outdir="$(cd "$2" && pwd)"
app="SD Sync Immich.app"
zip="SD-Sync-Immich.zip"

# The app's minimum macOS is 13.0 (LSMinimumSystemVersion): the Go 1.27
# linker marks its own objects for 13.0, so a lower target only makes ld
# warn. Wails' cgo objects otherwise target the SDK's macOS version, and ld
# warns about every one of them. "-O2 -g" keeps Go's default cgo flags.
export CGO_ENABLED=1
export CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0"
export CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0"

"$root/scripts/build-frontend.sh"

cd "$root"
ldflags="-s -w -X github.com/dmitiigorshkov/sd-sync-immich/internal/version.Current=$version"
# -tags production: without it Wails runs in development mode (web
# inspector, dev-server asset handling).
for arch in arm64 amd64; do
  GOOS=darwin GOARCH="$arch" go build -tags production -ldflags "$ldflags" \
    -o "$outdir/sd-sync-immich-darwin-$arch" ./cmd/sd-sync-immich
done
lipo -create -output "$outdir/sd-sync-immich" \
  "$outdir/sd-sync-immich-darwin-arm64" "$outdir/sd-sync-immich-darwin-amd64"
chmod 0755 "$outdir/sd-sync-immich" "$outdir/sd-sync-immich-darwin-arm64" "$outdir/sd-sync-immich-darwin-amd64"

# make-bundle only lays out and signs the bundle; it does not start the app.
rm -rf "${outdir:?}/$app" "$outdir/$zip"
"$outdir/sd-sync-immich" make-bundle "$outdir/$app"

cd "$outdir"
# --norsrc: no AppleDouble "._*" entries for the files' extended attributes
# (com.apple.provenance); unzip(1) would extract them as files into the
# bundle and break its signature.
ditto -c -k --norsrc --keepParent "$app" "$zip"
shasum -a 256 sd-sync-immich sd-sync-immich-darwin-arm64 sd-sync-immich-darwin-amd64 "$zip" > checksums.txt
echo "Built $version into $outdir"
