//go:build !darwin

package icon

import (
	"image"
	"image/draw"
)

// Template is false: the icon is the colored logo, shown as is.
const Template = false

// prepare keeps the colored logo as it is.
func prepare(src image.Image) image.Image {
	out := image.NewRGBA(src.Bounds())
	draw.Draw(out, out.Bounds(), src, src.Bounds().Min, draw.Src)
	return out
}
