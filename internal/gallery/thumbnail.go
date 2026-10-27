// Package gallery lists the photos on a card and serves them to the window.
package gallery

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"

	"golang.org/x/image/draw"
)

const (
	// headSize is how much of a file is read looking for the EXIF thumbnail.
	headSize = 128 << 10
	// thumbSide is the long side of a thumbnail made from the full image.
	thumbSide = 400
	// thumbQuality is the JPEG quality of re-encoded thumbnails.
	thumbQuality = 80
)

// Thumbnail returns a JPEG thumbnail of the photo at path. It prefers the
// camera's embedded EXIF thumbnail, read from the head of the file and turned
// upright by the main image's Orientation; when there is none, or it is
// broken, it decodes the whole photo and scales it to thumbSide px on the
// long side.
func Thumbnail(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, headSize)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	o, thumb := parseExif(head[:n])

	if thumb != nil {
		if img, err := jpeg.Decode(bytes.NewReader(thumb)); err == nil {
			if o == 1 {
				return thumb, nil
			}
			return encodeThumb(orient(img, o))
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	img, err := jpeg.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return encodeThumb(orient(scale(img, thumbSide), o))
}

func encodeThumb(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scale shrinks img so its long side is side px; smaller images are kept.
func scale(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	nw, nh := side, max(1, (h*side+w/2)/w)
	if h > w {
		nw, nh = max(1, (w*side+h/2)/h), side
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// orient turns img upright for EXIF Orientation o: 2 mirror horizontal,
// 3 rotate 180, 4 mirror vertical, 5 transpose, 6 rotate 90 CW,
// 7 transverse, 8 rotate 90 CCW. 1 and unknown values return img as is.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src, ok := img.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	}
	sb := src.Bounds()

	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.SetRGBA(dx, dy, src.RGBAAt(sb.Min.X+x, sb.Min.Y+y))
		}
	}
	return dst
}

// parseExif finds the APP1 Exif segment in the head of a JPEG and returns the
// main image's Orientation (1 when absent or invalid) and the embedded
// thumbnail (nil when absent). Broken data is never an error: whatever cannot
// be read is treated as missing.
func parseExif(head []byte) (orientation int, thumb []byte) {
	if len(head) < 2 || head[0] != 0xFF || head[1] != 0xD8 {
		return 1, nil
	}
	for pos := 2; pos+4 <= len(head); {
		if head[pos] != 0xFF {
			return 1, nil
		}
		marker := head[pos+1]
		switch {
		case marker == 0xFF: // fill byte
			pos++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7): // no length
			pos += 2
			continue
		case marker == 0xDA || marker == 0xD9: // image data starts: no EXIF
			return 1, nil
		}
		n := int(binary.BigEndian.Uint16(head[pos+2:]))
		if n < 2 {
			return 1, nil
		}
		end := min(pos+2+n, len(head))
		seg := head[pos+4 : end]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return parseTIFF(seg[6:])
		}
		pos += 2 + n
	}
	return 1, nil
}

// parseTIFF reads Orientation from IFD0 and the JPEG thumbnail from IFD1 of
// an EXIF TIFF block. Every offset is checked against len(t).
func parseTIFF(t []byte) (orientation int, thumb []byte) {
	orientation = 1
	if len(t) < 8 {
		return orientation, nil
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return orientation, nil
	}
	if order.Uint16(t[2:]) != 42 {
		return orientation, nil
	}

	ifd0, ok := readIFD(t, order, order.Uint32(t[4:]))
	if !ok {
		return orientation, nil
	}
	if v, ok := ifd0.value(0x0112); ok && v >= 1 && v <= 8 {
		orientation = int(v)
	}
	if ifd0.next == 0 {
		return orientation, nil
	}
	ifd1, ok := readIFD(t, order, ifd0.next)
	if !ok {
		return orientation, nil
	}
	off, ok1 := ifd1.value(0x0201)
	n, ok2 := ifd1.value(0x0202)
	if !ok1 || !ok2 || off == 0 || n == 0 || uint64(off)+uint64(n) > uint64(len(t)) {
		return orientation, nil
	}
	return orientation, t[off : off+n]
}

// ifd is one TIFF image file directory: its 12-byte entries and the offset
// of the next directory (0 when none).
type ifd struct {
	order   binary.ByteOrder
	entries []byte
	next    uint32
}

// readIFD reads the directory at off; ok is false when it lies outside t.
func readIFD(t []byte, order binary.ByteOrder, off uint32) (d ifd, ok bool) {
	if uint64(off)+2 > uint64(len(t)) {
		return ifd{}, false
	}
	count := uint64(order.Uint16(t[off:]))
	start := uint64(off) + 2
	end := start + count*12
	if end > uint64(len(t)) {
		return ifd{}, false
	}
	d = ifd{order: order, entries: t[start:end]}
	if end+4 <= uint64(len(t)) {
		d.next = order.Uint32(t[end:])
	}
	return d, true
}

// value returns the single SHORT or LONG value of tag.
func (d ifd) value(tag uint16) (uint32, bool) {
	for e := d.entries; len(e) >= 12; e = e[12:] {
		if d.order.Uint16(e) != tag {
			continue
		}
		if d.order.Uint32(e[4:]) != 1 {
			return 0, false
		}
		switch d.order.Uint16(e[2:]) {
		case 3: // SHORT
			return uint32(d.order.Uint16(e[8:])), true
		case 4: // LONG
			return d.order.Uint32(e[8:]), true
		}
		return 0, false
	}
	return 0, false
}
