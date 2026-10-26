#!/usr/bin/env bash
# Renders build/darwin/dmg/background.html into background.tiff next to it:
# one TIFF with the 1x (660 × 400) and 2x (1320 × 800) images, so Finder
# shows the sharp one on a Retina display. Run it by hand after editing the
# HTML and commit the TIFF; the release build only reads it.
#
# Needs Google Chrome (headless) and tiffutil (macOS).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/build/darwin/dmg"
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for scale in 1 2; do
  "$chrome" --headless --disable-gpu --hide-scrollbars --no-first-run \
    --user-data-dir="$tmp/profile" \
    --window-size=660,400 --force-device-scale-factor="$scale" \
    --screenshot="$tmp/bg-$scale.png" "file://$dir/background.html" 2>/dev/null
done
# 72 and 144 dpi mark the two images as 1x and 2x of the same 660 × 400.
sips -s dpiWidth 72 -s dpiHeight 72 "$tmp/bg-1.png" >/dev/null
sips -s dpiWidth 144 -s dpiHeight 144 "$tmp/bg-2.png" >/dev/null
tiffutil -cathidpicheck "$tmp/bg-1.png" "$tmp/bg-2.png" -out "$dir/background.tiff"
echo "Wrote $dir/background.tiff"
