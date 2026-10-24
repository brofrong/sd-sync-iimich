// Package icon draws the tray icon: IdlePNG, and SyncingPNG with a spinner
// badge. On macOS it is a monochrome template image (Template is true),
// which the menu bar tints; elsewhere it is the colored logo.
package icon

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sync"
)

//go:embed immich.png
var immichPNG []byte

const (
	Size       = 44
	FrameCount = 12
)

var (
	base     image.Image
	idlePNG  []byte
	baseOnce sync.Once
	cache    sync.Map // int -> []byte
)

func initBase() {
	src, err := png.Decode(bytes.NewReader(immichPNG))
	if err != nil {
		panic("gui icon: decode immich.png: " + err.Error())
	}
	base = prepare(src)
	idlePNG = encodePNG(base)
}

// IdlePNG returns the tray icon.
func IdlePNG() []byte {
	baseOnce.Do(initBase)
	return idlePNG
}

// SyncingPNG returns the tray icon with a monochrome spinner badge.
func SyncingPNG(frame int) []byte {
	baseOnce.Do(initBase)

	if frame < 0 {
		frame = 0
	}
	frame %= FrameCount

	if v, ok := cache.Load(frame); ok {
		return v.([]byte)
	}

	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(dst, dst.Bounds(), base, image.Point{}, draw.Src)
	drawSpinner(dst, frame)

	b := encodePNG(dst)
	cache.Store(frame, b)
	return b
}

func drawSpinner(img *image.RGBA, frame int) {
	const (
		badgeR = 14
		arcR   = 9
		dotR   = 3
		cx     = Size - 1 - badgeR
		cy     = Size - 1 - badgeR
	)

	// Subtle ring so the spinner reads on top of the logo in template mode.
	drawRing(img, cx, cy, badgeR, 1, color.RGBA{0, 0, 0, 90})

	start := float64(frame) / float64(FrameCount) * 2 * math.Pi
	sweep := math.Pi * 1.35
	steps := 20

	for i := 0; i < steps; i++ {
		t := float64(i) / float64(steps-1)
		angle := start + sweep*t
		alpha := uint8(60 + 195*t)
		x := cx + int(math.Round(float64(arcR)*math.Cos(angle)))
		y := cy + int(math.Round(float64(arcR)*math.Sin(angle)))
		fillCircle(img, x, y, dotR, color.RGBA{0, 0, 0, alpha})
	}
}

func drawRing(img *image.RGBA, cx, cy, r, thickness int, col color.Color) {
	outer := r + thickness
	inner := r - thickness
	if inner < 0 {
		inner = 0
	}
	outer2 := outer * outer
	inner2 := inner * inner

	for y := cy - outer; y <= cy+outer; y++ {
		if y < 0 || y >= Size {
			continue
		}
		for x := cx - outer; x <= cx+outer; x++ {
			if x < 0 || x >= Size {
				continue
			}
			dx, dy := x-cx, y-cy
			d2 := dx*dx + dy*dy
			if d2 <= outer2 && d2 >= inner2 {
				img.Set(x, y, col)
			}
		}
	}
}

func fillCircle(img *image.RGBA, cx, cy, r int, col color.Color) {
	r2 := r * r
	for y := cy - r; y <= cy+r; y++ {
		if y < 0 || y >= Size {
			continue
		}
		for x := cx - r; x <= cx+r; x++ {
			if x < 0 || x >= Size {
				continue
			}
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r2 {
				img.Set(x, y, col)
			}
		}
	}
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("gui icon: encode png: " + err.Error())
	}
	return buf.Bytes()
}
