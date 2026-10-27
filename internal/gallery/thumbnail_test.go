package gallery

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	blue = color.RGBA{0, 0, 255, 255}
	red  = color.RGBA{255, 0, 0, 255}
)

// Offsets inside the TIFF block built by buildTIFF.
const (
	tiffIFD0Off     = 4  // header: offset of IFD0
	tiffOrientValue = 18 // IFD0 entry 0x0112: value
	tiffIFD1Off     = 22 // IFD0: offset of the next IFD (IFD1)
	tiffThumbOff    = 36 // IFD1 entry 0x0201: value
	tiffThumbLen    = 48 // IFD1 entry 0x0202: value
	tiffThumbData   = 56 // thumbnail bytes
)

// encodeJPEG returns a w×h JPEG filled with c.
func encodeJPEG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// buildTIFF returns a TIFF block with IFD0 holding Orientation and IFD1
// pointing at thumb, laid out at the tiff* offsets above.
func buildTIFF(order binary.ByteOrder, orientation uint16, thumb []byte) []byte {
	b := make([]byte, tiffThumbData, tiffThumbData+len(thumb))
	if order == binary.BigEndian {
		copy(b, "MM")
	} else {
		copy(b, "II")
	}
	order.PutUint16(b[2:], 42)
	order.PutUint32(b[tiffIFD0Off:], 8)

	// IFD0: one entry, Orientation SHORT.
	order.PutUint16(b[8:], 1)
	order.PutUint16(b[10:], 0x0112)
	order.PutUint16(b[12:], 3)
	order.PutUint32(b[14:], 1)
	order.PutUint16(b[tiffOrientValue:], orientation)
	order.PutUint32(b[tiffIFD1Off:], 26)

	// IFD1: JPEGInterchangeFormat and its length, both LONG.
	order.PutUint16(b[26:], 2)
	order.PutUint16(b[28:], 0x0201)
	order.PutUint16(b[30:], 4)
	order.PutUint32(b[32:], 1)
	order.PutUint32(b[tiffThumbOff:], tiffThumbData)
	order.PutUint16(b[40:], 0x0202)
	order.PutUint16(b[42:], 4)
	order.PutUint32(b[44:], 1)
	order.PutUint32(b[tiffThumbLen:], uint32(len(thumb)))
	order.PutUint32(b[52:], 0)

	return append(b, thumb...)
}

// withExif inserts an APP1 "Exif\0\0" segment carrying tiff right after SOI.
func withExif(jpg, tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(2+len(payload)))
	seg = append(seg, payload...)

	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "DSC00001.JPG")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// decode decodes a JPEG result and checks its size.
func decode(t *testing.T, data []byte, w, h int) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("result is not a JPEG: %v", err)
	}
	if got := img.Bounds().Size(); got != image.Pt(w, h) {
		t.Fatalf("result size = %v, want %dx%d", got, w, h)
	}
	return img
}

func assertColor(t *testing.T, img image.Image, want color.RGBA) {
	t.Helper()
	b := img.Bounds()
	r, g, bl, _ := img.At((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2).RGBA()
	near := func(got uint32, want uint8) bool {
		d := int(got>>8) - int(want)
		return d > -40 && d < 40
	}
	if !near(r, want.R) || !near(g, want.G) || !near(bl, want.B) {
		t.Fatalf("centre colour = (%d,%d,%d), want about %v", r>>8, g>>8, bl>>8, want)
	}
}

func TestThumbnailUsesExifThumbnail(t *testing.T) {
	thumb := encodeJPEG(t, 160, 106, red)
	file := withExif(encodeJPEG(t, 1200, 800, blue), buildTIFF(binary.LittleEndian, 1, thumb))

	got, err := Thumbnail(writeTemp(t, file))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 160, 106), red)
	if !bytes.Equal(got, thumb) {
		t.Error("Orientation 1: want the embedded thumbnail bytes as is")
	}
}

func TestThumbnailRotatesByOrientation(t *testing.T) {
	thumb := encodeJPEG(t, 160, 106, red)
	file := withExif(encodeJPEG(t, 1200, 800, blue), buildTIFF(binary.LittleEndian, 6, thumb))

	got, err := Thumbnail(writeTemp(t, file))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 106, 160), red)
}

func TestThumbnailWithoutExif(t *testing.T) {
	got, err := Thumbnail(writeTemp(t, encodeJPEG(t, 2000, 1000, blue)))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 400, 200), blue)
}

func TestThumbnailSmallImageNotEnlarged(t *testing.T) {
	got, err := Thumbnail(writeTemp(t, encodeJPEG(t, 300, 100, blue)))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	decode(t, got, 300, 100)
}

func TestThumbnailBrokenExif(t *testing.T) {
	thumb := encodeJPEG(t, 160, 106, red)
	le := binary.LittleEndian
	valid := func() []byte { return buildTIFF(le, 6, thumb) }

	cases := []struct {
		name string
		tiff func() []byte
		w, h int // the fallback applies Orientation when IFD0 survives
	}{
		{"empty TIFF", func() []byte { return nil }, 400, 200},
		{"short TIFF header", func() []byte { return []byte("II*") }, 400, 200},
		{"bad byte order", func() []byte { b := valid(); copy(b, "XX"); return b }, 400, 200},
		{"bad magic", func() []byte { b := valid(); le.PutUint16(b[2:], 43); return b }, 400, 200},
		{"IFD0 out of range", func() []byte { b := valid(); le.PutUint32(b[tiffIFD0Off:], 0xFFFFFFF0); return b }, 400, 200},
		{"IFD0 truncated", func() []byte { return valid()[:14] }, 400, 200},
		{"IFD0 huge entry count", func() []byte { b := valid(); le.PutUint16(b[8:], 0xFFFF); return b }, 400, 200},
		{"orientation out of range", func() []byte {
			b := valid()
			le.PutUint16(b[tiffOrientValue:], 9)
			le.PutUint32(b[tiffIFD1Off:], 0)
			return b
		}, 400, 200},
		{"IFD1 out of range", func() []byte { b := valid(); le.PutUint32(b[tiffIFD1Off:], 0xFFFFFFFF); return b }, 200, 400},
		{"IFD1 truncated", func() []byte { return valid()[:34] }, 200, 400},
		{"thumbnail offset out of range", func() []byte { b := valid(); le.PutUint32(b[tiffThumbOff:], 0xFFFFFFF0); return b }, 200, 400},
		{"thumbnail length overflows", func() []byte { b := valid(); le.PutUint32(b[tiffThumbLen:], 0xFFFFFFFF); return b }, 200, 400},
		{"thumbnail cut short", func() []byte { b := valid(); return b[:len(b)-len(thumb)/2] }, 200, 400},
		{"thumbnail not a JPEG", func() []byte { return buildTIFF(le, 6, []byte("not a jpeg at all")) }, 200, 400},
	}
	main := encodeJPEG(t, 2000, 1000, blue)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Thumbnail(writeTemp(t, withExif(main, tc.tiff())))
			if err != nil {
				t.Fatalf("Thumbnail() error = %v", err)
			}
			assertColor(t, decode(t, got, tc.w, tc.h), blue)
		})
	}
}

func TestThumbnailBrokenSegment(t *testing.T) {
	main := encodeJPEG(t, 2000, 1000, blue)
	for _, payload := range []string{"", "Ex", "Exif\x00\x00", "Exif\x00\x00II*\x00"} {
		seg := []byte{0xFF, 0xE1, 0, byte(2 + len(payload))}
		file := append(append(append([]byte{0xFF, 0xD8}, seg...), payload...), main[2:]...)
		got, err := Thumbnail(writeTemp(t, file))
		if err != nil {
			t.Fatalf("payload %q: Thumbnail() error = %v", payload, err)
		}
		decode(t, got, 400, 200)
	}

	// A segment length running past the end of the file must not panic;
	// the main image is unreadable then, so an error is expected.
	file := append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, "Exif\x00\x00II*\x00"...)
	if _, err := Thumbnail(writeTemp(t, file)); err == nil {
		t.Fatal("Thumbnail() error = nil for a truncated file")
	}
}

func TestThumbnailBigEndian(t *testing.T) {
	thumb := encodeJPEG(t, 160, 106, red)
	main := encodeJPEG(t, 1200, 800, blue)

	got, err := Thumbnail(writeTemp(t, withExif(main, buildTIFF(binary.BigEndian, 1, thumb))))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 160, 106), red)
	if !bytes.Equal(got, thumb) {
		t.Error("Orientation 1: want the embedded thumbnail bytes as is")
	}

	got, err = Thumbnail(writeTemp(t, withExif(main, buildTIFF(binary.BigEndian, 8, thumb))))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 106, 160), red)
}

func TestThumbnailMissingFile(t *testing.T) {
	if _, err := Thumbnail(filepath.Join(t.TempDir(), "missing.JPG")); err == nil {
		t.Fatal("Thumbnail() error = nil for a missing file")
	}
}

func TestThumbnailNotAJPEG(t *testing.T) {
	if _, err := Thumbnail(writeTemp(t, []byte("plain text"))); err == nil {
		t.Fatal("Thumbnail() error = nil for a non-JPEG file")
	}
}

func TestOrient(t *testing.T) {
	// A 3×2 source: top-left red, top-right green, the rest black.
	green := color.RGBA{0, 255, 0, 255}
	src := image.NewRGBA(image.Rect(10, 20, 13, 22)) // non-zero origin on purpose
	for y := 20; y < 22; y++ {
		for x := 10; x < 13; x++ {
			src.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	src.Set(10, 20, red)
	src.Set(12, 20, green)

	cases := []struct {
		o          int
		w, h       int
		red, green image.Point
	}{
		{1, 3, 2, image.Pt(0, 0), image.Pt(2, 0)},
		{2, 3, 2, image.Pt(2, 0), image.Pt(0, 0)},
		{3, 3, 2, image.Pt(2, 1), image.Pt(0, 1)},
		{4, 3, 2, image.Pt(0, 1), image.Pt(2, 1)},
		{5, 2, 3, image.Pt(0, 0), image.Pt(0, 2)},
		{6, 2, 3, image.Pt(1, 0), image.Pt(1, 2)},
		{7, 2, 3, image.Pt(1, 2), image.Pt(1, 0)},
		{8, 2, 3, image.Pt(0, 2), image.Pt(0, 0)},
		{0, 3, 2, image.Pt(0, 0), image.Pt(2, 0)}, // unknown → as is
		{9, 3, 2, image.Pt(0, 0), image.Pt(2, 0)},
	}
	for _, tc := range cases {
		got := orient(src, tc.o)
		b := got.Bounds()
		if b.Dx() != tc.w || b.Dy() != tc.h {
			t.Errorf("orient(%d) size = %dx%d, want %dx%d", tc.o, b.Dx(), b.Dy(), tc.w, tc.h)
			continue
		}
		at := func(p image.Point) color.RGBA {
			return color.RGBAModel.Convert(got.At(b.Min.X+p.X, b.Min.Y+p.Y)).(color.RGBA)
		}
		if c := at(tc.red); c != red {
			t.Errorf("orient(%d) at %v = %v, want red", tc.o, tc.red, c)
		}
		if c := at(tc.green); c != green {
			t.Errorf("orient(%d) at %v = %v, want green", tc.o, tc.green, c)
		}
	}
}

func TestThumbnailDoesNotPinHeadBuffer(t *testing.T) {
	thumb := encodeJPEG(t, 160, 106, red)
	file := withExif(encodeJPEG(t, 1200, 800, blue), buildTIFF(binary.LittleEndian, 1, thumb))

	got, err := Thumbnail(writeTemp(t, file))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	// A sub-slice of the head buffer would keep all of it alive in the cache.
	if cap(got) >= headSize/2 {
		t.Fatalf("cap(result) = %d for a %d-byte thumbnail: it pins the %d-byte head buffer", cap(got), len(got), headSize)
	}
}

func TestThumbnailIgnoresHugeEmbeddedThumbnail(t *testing.T) {
	// 2100×2000 is over the embedded thumbnail limit; one flat grey plane
	// keeps it small enough to fit in an APP1 segment.
	img := image.NewGray(image.Rect(0, 0, 2100, 2000))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	file := withExif(encodeJPEG(t, 2000, 1000, blue), buildTIFF(binary.LittleEndian, 1, buf.Bytes()))

	got, err := Thumbnail(writeTemp(t, file))
	if err != nil {
		t.Fatalf("Thumbnail() error = %v", err)
	}
	assertColor(t, decode(t, got, 400, 200), blue)
}

// withSize rewrites the frame size in a baseline JPEG's SOF0 header.
func withSize(t *testing.T, jpg []byte, w, h int) []byte {
	t.Helper()
	out := bytes.Clone(jpg)
	i := bytes.Index(out, []byte{0xFF, 0xC0})
	if i < 0 || i+9 > len(out) {
		t.Fatal("no SOF0 marker")
	}
	binary.BigEndian.PutUint16(out[i+5:], uint16(h))
	binary.BigEndian.PutUint16(out[i+7:], uint16(w))
	return out
}

func TestThumbnailRefusesHugeImage(t *testing.T) {
	// 10000×10000 claims 100 megapixels: decoding would allocate ~150 MB.
	file := withSize(t, encodeJPEG(t, 64, 64, blue), 10000, 10000)

	_, err := Thumbnail(writeTemp(t, file))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("Thumbnail() error = %v, want a too-large error", err)
	}
}
