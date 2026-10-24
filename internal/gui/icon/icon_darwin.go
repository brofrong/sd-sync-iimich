package icon

import (
	"image"
	"image/color"
)

// Template is true: the icon is a macOS menu bar template image (black
// and alpha), which the menu bar tints to match its appearance.
const Template = true

// prepare turns the colored logo into the template image.
func prepare(src image.Image) image.Image { return toTemplate(src) }

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
