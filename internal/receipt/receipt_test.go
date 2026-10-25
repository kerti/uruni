package receipt

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"runtime"
	"testing"
)

// --- image fixtures ---------------------------------------------------------

// solidBlockImage is the shared image builder: bg everywhere with a solid
// block of another color top-left, simple enough that JPEG's lossy 8x8-block
// compression at quality 80 (receipt.go) leaves it distinguishable.
func solidBlockImage(w, h, blockW, blockH int, bg, block color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, blockW, blockH), &image.Uniform{C: block}, image.Point{}, draw.Src)
	return img
}

func encodeTestJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encoding fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

// withEXIFOrientation splices a minimal hand-built APP1 Exif segment (a TIFF
// header, one IFD0 entry naming Orientation 0x0112) after jpegBytes' SOI. It
// is the structure jpegOrientation reads, built independently of this
// package's encoder so the test does not check the code against itself.
func withEXIFOrientation(t testing.TB, jpegBytes []byte, orientation byte) []byte {
	t.Helper()
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		t.Fatalf("fixture does not start with a JPEG SOI marker")
	}

	tiff := []byte{
		'I', 'I', 0x2A, 0x00, // little-endian TIFF header, magic 0x002A
		0x08, 0x00, 0x00, 0x00, // IFD0 offset (right after this header)
		0x01, 0x00, // one entry
		0x12, 0x01, // tag 0x0112 - Orientation
		0x03, 0x00, // type 3 - SHORT
		0x01, 0x00, 0x00, 0x00, // count 1
		orientation, 0x00, 0x00, 0x00, // value (first 2 bytes) + padding
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := make([]byte, 2)
	binary.BigEndian.PutUint16(segLen, uint16(len(payload)+2)) //nolint:gosec // payload is this function's own fixed ~32-byte TIFF fixture, nowhere near uint16's range
	app1 := append([]byte{0xFF, 0xE1}, segLen...)
	app1 = append(app1, payload...)

	out := make([]byte, 0, len(jpegBytes)+len(app1))
	out = append(out, jpegBytes[:2]...) // SOI
	out = append(out, app1...)
	out = append(out, jpegBytes[2:]...)
	return out
}

var (
	fixtureBG    = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	fixtureBlock = color.RGBA{R: 220, G: 20, B: 20, A: 255}
)

// closerToRed reports whether c reads much closer to fixtureBlock (a strong
// red) than fixtureBG (white): the tolerant check pixel assertions use, as
// re-encoding at quality 80 never round-trips exact values.
func closerToRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	// RGBA() returns 16-bit-scaled channels; red should dominate clearly.
	return r > 40000 && g < 40000 && b < 40000
}

func closerToWhite(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 55000 && g > 55000 && b > 55000
}

// TestALargeSidewaysPhotoIsRotatedAfterItIsScaled pins #531: the EXIF rotation
// runs on the downscaled image, never on the full-resolution one. The old order
// built a full-size RGBA copy pixel by pixel - about 200 MB at the 50 MP cap -
// and the resample after it kept a float64 buffer of output width by source
// height. Now the run allocates little beyond what decoding costs on its own.
// Not parallel: the bound reads process-wide
// allocation, so nothing else may run alongside.
func TestALargeSidewaysPhotoIsRotatedAfterItIsScaled(t *testing.T) {
	const w, h = 8000, 1000
	plain := encodeTestJPEG(t, solidBlockImage(w, h, h, h, fixtureBG, fixtureBlock))
	fixture := withEXIFOrientation(t, plain, 6)

	// The least of three runs: TotalAlloc is process-wide, and a goroutine an
	// earlier test left behind can add a few MB to any one reading.
	allocatedBy := func(f func()) uint64 {
		least := uint64(math.MaxUint64)
		for range 3 {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			f()
			runtime.ReadMemStats(&after)
			least = min(least, after.TotalAlloc-before.TotalAlloc)
		}
		return least
	}
	decodeOnly := allocatedBy(func() { _, _, _ = image.Decode(bytes.NewReader(fixture)) })
	var out []byte
	var err error
	whole := allocatedBy(func() { out, err = Process(fixture) })
	if err != nil {
		t.Fatalf("Process() = %v, want no error", err)
	}
	// Past the decode, one full-resolution RGBA copy is w*h*4 bytes; the bound
	// is half that. On this image the old pipeline spent about 150 MB here,
	// scaling first with the kernel resample still about 55 MB, and block
	// averages cost about 3 MB.
	if beyond, budget := whole-min(whole, decodeOnly), uint64(w*h*2); beyond >= budget {
		t.Errorf("allocated %d bytes beyond the decode's %d, want under %d: something works at full resolution", beyond, decodeOnly, budget)
	}

	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decoding the result: %v", err)
	}
	// 8000x1000 scales to 1600x200, and rotating 90 CW makes it 200x1600 with
	// the left-hand block across the top.
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 1600 {
		t.Fatalf("result = %dx%d, want 200x1600", b.Dx(), b.Dy())
	}
	if !closerToRed(img.At(100, 100)) {
		t.Errorf("top of the rotated image = %v, want the block (red)", img.At(100, 100))
	}
	if !closerToWhite(img.At(100, 1500)) {
		t.Errorf("bottom of the rotated image = %v, want the background (white)", img.At(100, 1500))
	}
}
