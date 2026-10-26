#!/usr/bin/env bash
# Renders build/icon/app.svg and tray.svg into every icon the app ships:
#
#   internal/bundle/icons.icns           macOS app (and the DMG's volume)
#   build/windows/icon.ico               Windows executable and installer
#   build/linux/sd-sync-immich.png       Linux AppImage
#   internal/gui/icon/tray/template-NN.png  macOS menu bar (template) frames
#   internal/gui/icon/tray/color-NN.png     Windows and Linux tray frames
#
# A tray frame turns the ring of sync arrows by NN × 15°; FrameCount (12)
# frames make a half turn, after which the ring looks the same. Windows,
# Linux and the tray use the icon cropped to its body, without the macOS
# margin. Run it by hand after editing the SVGs and commit the output; the
# release build only reads it.
#
# Needs Google Chrome (headless), ImageMagick and iconutil (macOS).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
src="$root/build/icon"
tray="$root/internal/gui/icon/tray"
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
frames=12
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# render SVG PIXELS OUT [RING_DEGREES] [crop]: SVG drawn at PIXELS square
# on a transparent background, with its ring turned and optionally cropped
# to the app icon's body.
render() {
  local svg="$1" px="$2" out="$3" deg="${4:-0}" crop="${5:-}"
  local page="$tmp/page.svg"
  sed -e "s/rotate(0 512 512)/rotate($deg 512 512)/" \
    -e "s/rotate(0 22 22)/rotate($deg 22 22)/" "$svg" >"$page"
  if [[ -n "$crop" ]]; then
    sed -i '' 's/viewBox="0 0 1024 1024"/viewBox="100 100 824 824"/' "$page"
  fi
  "$chrome" --headless --disable-gpu --hide-scrollbars --no-first-run \
    --user-data-dir="$tmp/profile" --default-background-color=00000000 \
    --window-size="$px,$px" --screenshot="$out" "file://$page" 2>/dev/null
}

# macOS: the full 1024 grid, margin included, at every iconset size.
render "$src/app.svg" 1024 "$tmp/app.png"
set="$tmp/icons.iconset"
mkdir "$set"
for s in 16 32 128 256 512; do
  magick "$tmp/app.png" -resize "${s}x${s}" "PNG32:$set/icon_${s}x${s}.png"
  magick "$tmp/app.png" -resize "$((s * 2))x$((s * 2))" "PNG32:$set/icon_${s}x${s}@2x.png"
done
iconutil -c icns -o "$root/internal/bundle/icons.icns" "$set"

# Windows and Linux: the body alone.
render "$src/app.svg" 1024 "$tmp/body.png" 0 crop
ico=()
for s in 16 20 24 32 40 48 64 128 256; do
  magick "$tmp/body.png" -resize "${s}x${s}" "PNG32:$tmp/ico-$s.png"
  ico+=("$tmp/ico-$s.png")
done
# An ICO of PNG images, as Windows Vista and later read them (ImageMagick
# would write the small sizes as 8-bit bitmaps).
python3 -I - "$root/build/windows/icon.ico" "${ico[@]}" <<'EOF'
import struct, sys
out, pngs = sys.argv[1], [open(p, "rb").read() for p in sys.argv[2:]]
head = struct.pack("<HHH", 0, 1, len(pngs))
offset, entries = 6 + 16 * len(pngs), b""
for png in pngs:
    w, h = struct.unpack(">II", png[16:24])
    entries += struct.pack("<BBBBHHII", w % 256, h % 256, 0, 0, 1, 32, len(png), offset)
    offset += len(png)
open(out, "wb").write(head + entries + b"".join(pngs))
EOF
magick "$tmp/body.png" -resize 512x512 "PNG32:$root/build/linux/sd-sync-immich.png"

# Tray frames: 44 pixels (22 points @2x) for the macOS menu bar, black and
# alpha only; 64 pixels in color for the Windows and Linux trays.
mkdir -p "$tray"
rm -f "$tray"/*.png
for ((i = 0; i < frames; i++)); do
  n="$(printf %02d "$i")"
  render "$src/tray.svg" 440 "$tmp/tray.png" $((i * 15))
  magick "$tmp/tray.png" -resize 44x44 -channel RGB -evaluate set 0 +channel \
    "PNG32:$tray/template-$n.png"
  render "$src/app.svg" 1024 "$tmp/frame.png" $((i * 15)) crop
  magick "$tmp/frame.png" -resize 64x64 "PNG32:$tray/color-$n.png"
done
echo "Wrote the icons from $src"
