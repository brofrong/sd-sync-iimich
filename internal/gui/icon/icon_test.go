package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestFrames checks every frame is a square PNG of the idle icon's size,
// each differs from the one before (the ring turns), and the frame number
// wraps around.
func TestFrames(t *testing.T) {
	idle := decode(t, IdlePNG()).Bounds()
	if idle.Dx() != idle.Dy() || idle.Dx() == 0 {
		t.Fatalf("idle icon is %v, want a square", idle)
	}
	for i := 0; i < FrameCount; i++ {
		if b := decode(t, SyncingPNG(i)).Bounds(); b != idle {
			t.Fatalf("frame %d is %v, want %v", i, b, idle)
		}
		if i > 0 && bytes.Equal(SyncingPNG(i), SyncingPNG(i-1)) {
			t.Fatalf("frame %d is the same as frame %d", i, i-1)
		}
	}
	if !bytes.Equal(SyncingPNG(FrameCount+1), SyncingPNG(1)) {
		t.Fatal("frame FrameCount+1 is not frame 1")
	}
	if !bytes.Equal(SyncingPNG(-1), IdlePNG()) {
		t.Fatal("frame -1 is not the idle icon")
	}
}

// TestTemplateFrames checks the macOS frames are template images: black,
// with the shape in alpha only.
func TestTemplateFrames(t *testing.T) {
	if !Template {
		t.Skip("the tray icon is colored on this platform")
	}
	for i := 0; i < FrameCount; i++ {
		img := decode(t, SyncingPNG(i))
		opaque := 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				if c.A > 0 && (c.R != 0 || c.G != 0 || c.B != 0) {
					t.Fatalf("frame %d: pixel (%d, %d) is %v, want black", i, x, y, c)
				}
				if c.A == 0xff {
					opaque++
				}
			}
		}
		if opaque == 0 {
			t.Fatalf("frame %d has no opaque pixels", i)
		}
	}
}
