//go:build darwin

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
	base = toTemplate(src)
	idlePNG = encodePNG(base)
}

// IdlePNG returns the monochrome macOS template tray icon.
func IdlePNG() []byte {
	baseOnce.Do(initBase)
	return idlePNG
}

// SyncingPNG returns the template icon with a monochrome spinner badge.
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

// toTemplate converts a color icon into a macOS menu bar template (black + alpha).
func toTemplate(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	out := image.NewRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			ar := uint8(a >> 8)
			if ar < 16 {
				continue
			}

			rr, gg, bb := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			maxC := max8(rr, max8(gg, bb))
			// Drop near-black background from the source favicon.
			if maxC < 36 {
				continue
			}

			lum := (int(rr)*299 + int(gg)*587 + int(bb)*114) / 1000
			alpha := uint8(min(255, lum*int(ar)/200+48))
			if alpha < 24 {
				continue
			}
			out.Set(x, y, color.RGBA{0, 0, 0, alpha})
		}
	}

	return out
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

func max8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
