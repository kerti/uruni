// Package receipt turns an uploaded receipt photo into what is stored:
// EXIF-corrected, downscaled, always re-encoded as JPEG with the metadata
// stripped. It owns the bytes-to-bytes transform and its rejections; it knows
// nothing of HTTP, the uploads volume or the receipt table, so callers map its
// errors to their own wire codes.
package receipt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder with image.Decode (blank import, decode-only)
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder with image.Decode (blank import, decode-only - there is no Go WebP encoder in this module, and receipts are always re-encoded as JPEG below)

	"github.com/kerti/uruni/internal/backup"
)

// maxReceiptLongEdge and receiptJPEGQuality (#153, ADR-011 "downscale on
// upload"): resize so the long edge is at most 1600px, never upscale, and
// always re-encode as JPEG at roughly quality 80. A phone photo is typically
// 3000-4000px and several megabytes; a receipt needs to be legible, not
// archival (PRD section 7.4: optional evidence, not a primary record).
const (
	maxReceiptLongEdge = 1600
	receiptJPEGQuality = 80
)

// ErrUnsupportedMediaType and ErrHEICUnsupported are the two
// distinct rejections: any format this package cannot decode, and HEIC/HEIF
// specifically. A phone defaulting to HEIC is the most common reason an
// upload reaches this path, so the treasurer needs to know to re-export as
// JPEG, not just that something was wrong.
var (
	ErrUnsupportedMediaType = errors.New("unsupported receipt media type")
	ErrHEICUnsupported      = errors.New("HEIC/HEIF receipts are not supported")
	ErrTooManyPixels        = errors.New("receipt image dimensions too large")
)

// Process turns an uploaded file's raw bytes into what is written
// to disk: EXIF-corrected, downscaled if needed, always re-encoded as JPEG.
//
// Re-encoding unconditionally, even a file within the limits, is deliberate:
// it strips the EXIF/GPS metadata phones embed (CLAUDE.md rule 6, data
// minimization; PRD section 6 asks for names, amounts, dates and notes, not
// GPS coordinates). The orientation tag is read from the raw bytes before that
// strip, in jpegOrientation, or the rotation would be lost with the EXIF
// block.
//
// The rotation runs after the downscale, on at most 1600px a side (#531).
// applyOrientation copies pixel by pixel into a new RGBA image, which at the
// 50 MP cap was about 200 MB before a single pixel was dropped. Rotating
// after scaling gives the same dimensions and the same picture, up to
// resampling rounding.
func Process(raw []byte) ([]byte, error) {
	if isHEIC(raw) {
		return nil, ErrHEICUnsupported
	}

	// DecodeConfig reads only the header, so the dimension check is free and
	// runs before any decoder sizes a pixel buffer.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrUnsupportedMediaType
	}
	if int64(cfg.Width)*int64(cfg.Height) > backup.MaxReceiptPixels {
		return nil, ErrTooManyPixels
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrUnsupportedMediaType
	}

	img = downscale(img)

	// Only a JPEG carries the EXIF orientation tag phones use for "held
	// sideways" instead of rotating pixels. image/jpeg drops that tag when
	// decoding, so it is read from the raw bytes.
	if format == "jpeg" {
		if orientation := jpegOrientation(raw); orientation > 1 {
			img = applyOrientation(img, orientation)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: receiptJPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// downscale resizes img so its long edge is at most maxReceiptLongEdge, never
// upscaling. An image already within the limit is returned as it is.
//
// Memory is the constraint (#531): a 50 MP upload is allowed, and the decoded
// image is already 75 MB (a JPEG's YCbCr) to 200 MB (a PNG's RGBA). A kernel
// resample such as draw.CatmullRom keeps a float64 buffer of output width by
// source height - about 330 MB for a 50 MP portrait photo - so it is not used
// on the full image. Instead the source is first averaged down by a whole
// factor (boxShrink, which allocates only its output), and the remaining step,
// under 2x, is a bilinear one with no intermediate buffer. Averaging blocks is
// the standard way to shrink text without aliasing; for a receipt that must be
// legible, not archival, the result is as good.
func downscale(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	longEdge := max(w, h)
	if longEdge <= maxReceiptLongEdge {
		return img
	}

	if k := longEdge / maxReceiptLongEdge; k >= 2 {
		img = boxShrink(img, k)
		b = img.Bounds()
		w, h = b.Dx(), b.Dy()
		longEdge = max(w, h)
		if longEdge <= maxReceiptLongEdge {
			return img
		}
	}

	scale := float64(maxReceiptLongEdge) / float64(longEdge)
	newW := max(1, int(math.Round(float64(w)*scale)))
	newH := max(1, int(math.Round(float64(h)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// boxShrink averages each k-by-k block of src into one pixel; a partial block
// at the right or bottom edge is dropped (at most k-1 pixels of a photo's
// margin). A JPEG decodes to *image.YCbCr, read here plane by plane; any other
// image goes through At, which is slower but rare (PNG and WebP receipts).
func boxShrink(src image.Image, k int) *image.RGBA {
	b := src.Bounds()
	dw, dh := b.Dx()/k, b.Dy()/k
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// k is at most the long edge over 1600 (about 5 at the 50 MP cap), and each
	// sum over n samples divided by n is back within the sample's own range, so
	// none of the narrowing conversions below can overflow.
	n := uint32(k * k) //nolint:gosec // see above

	if ycc, ok := src.(*image.YCbCr); ok {
		for dy := 0; dy < dh; dy++ {
			for dx := 0; dx < dw; dx++ {
				var ys, cbs, crs uint32
				for y := b.Min.Y + dy*k; y < b.Min.Y+(dy+1)*k; y++ {
					for x := b.Min.X + dx*k; x < b.Min.X+(dx+1)*k; x++ {
						ys += uint32(ycc.Y[ycc.YOffset(x, y)])
						ci := ycc.COffset(x, y)
						cbs += uint32(ycc.Cb[ci])
						crs += uint32(ycc.Cr[ci])
					}
				}
				r, g, bl := color.YCbCrToRGB(uint8(ys/n), uint8(cbs/n), uint8(crs/n)) //nolint:gosec // averages of uint8 samples
				dst.SetRGBA(dx, dy, color.RGBA{R: r, G: g, B: bl, A: 0xff})
			}
		}
		return dst
	}

	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var rs, gs, bs, as uint32
			for y := b.Min.Y + dy*k; y < b.Min.Y+(dy+1)*k; y++ {
				for x := b.Min.X + dx*k; x < b.Min.X+(dx+1)*k; x++ {
					r, g, bl, a := src.At(x, y).RGBA()
					rs, gs, bs, as = rs+r, gs+g, bs+bl, as+a
				}
			}
			dst.SetRGBA(dx, dy, color.RGBA{R: uint8(rs / n >> 8), G: uint8(gs / n >> 8), B: uint8(bs / n >> 8), A: uint8(as / n >> 8)}) //nolint:gosec // averages of 16-bit samples, shifted to 8
		}
	}
	return dst
}

// isHEIC sniffs for the ISO base media "ftyp" box HEIC/HEIF photos are
// wrapped in and the brand codes iPhones (and Android's HEIF support) write,
// never the filename or multipart Content-Type, which the client controls and
// which do not decide whether image.Decode can read the bytes.
//
// The layout is a 4-byte big-endian size, the ASCII "ftyp", then a 4-byte
// major brand. avif/avis (a sibling container ADR-011 does not name) are left
// out: they fall through to image.Decode, fail, and report as the generic
// ErrUnsupportedMediaType.
func isHEIC(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	if string(b[4:8]) != "ftyp" {
		return false
	}
	switch string(b[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs", "mif1", "msf1":
		return true
	default:
		return false
	}
}

// jpegOrientation reads the EXIF orientation tag (0x0112) from raw JPEG
// bytes, or returns 1 (normal, "do nothing") if there is no EXIF block, no
// tag, or the structure fails to parse. A malformed or absent block is not
// worth failing on: a photo with no tag is not sideways, it just does not
// say so.
//
// A small hand-rolled reader, not an EXIF library (#153): the feature needs
// one SHORT tag, and a general parser is a much larger dependency.
func jpegOrientation(raw []byte) int {
	// JPEG: a stream of markers, each 0xFF plus a one-byte code. SOI (0xFFD8) has
	// no length or payload; every marker from APP0 (0xFFE0) on is followed by a
	// 2-byte big-endian length (itself included) and that many payload bytes.
	// SOS (0xFFDA) starts the image data, so scanning stops there rather than
	// entering an entropy-coded scan that contains 0xFF bytes of its own.
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return 1
	}

	pos := 2
	for pos+4 <= len(raw) {
		if raw[pos] != 0xFF {
			return 1 // not a well-formed marker sequence - give up quietly
		}
		marker := raw[pos+1]
		if marker == 0xDA { // SOS: image data follows, no more markers to read
			return 1
		}
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) {
			// Standalone markers with no length or payload.
			pos += 2
			continue
		}

		segLen := int(binary.BigEndian.Uint16(raw[pos+2 : pos+4]))
		if segLen < 2 || pos+2+segLen > len(raw) {
			return 1
		}
		payload := raw[pos+4 : pos+2+segLen]

		if marker == 0xE1 && len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
			if o := orientationFromTIFF(payload[6:]); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}

		pos += 2 + segLen
	}
	return 1
}

// orientationFromTIFF reads the Orientation tag (0x0112) from a TIFF header +
// IFD0, which a JPEG's EXIF APP1 payload wraps after its "Exif\0\0" prefix.
// Returns 0 if the tag is absent or parsing fails; jpegOrientation treats
// that as no EXIF.
func orientationFromTIFF(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}

	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 0x002A {
		return 0
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset+2 > len(tiff) {
		return 0
	}

	entryCount := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	base := ifdOffset + 2
	const entrySize = 12
	for i := 0; i < entryCount; i++ {
		start := base + i*entrySize
		if start+entrySize > len(tiff) {
			return 0
		}
		tag := order.Uint16(tiff[start : start+2])
		if tag != 0x0112 { // Orientation
			continue
		}
		// SHORT (type 3): the value sits in the first 2 bytes of the 4-byte
		// value/offset field, never a separate offset.
		valueField := tiff[start+8 : start+12]
		return int(order.Uint16(valueField[:2]))
	}
	return 0
}

// applyOrientation returns a new image with EXIF orientation o (2-8) applied,
// or img unchanged for 1 (normal) or anything outside the valid range. An
// explicit per-orientation pixel remap, not an affine transform: eight fixed
// cases are easier to verify by hand and to unit-test.
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Orientations 5-8 rotate 90 degrees and swap width and height; 2-4 only
	// mirror or rotate 180.
	dstW, dstH := w, h
	if o >= 5 {
		dstW, dstH = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose (mirror horizontal + rotate 270 CW)
				dx, dy = y, x
			case 6: // rotate 90 CW
				dx, dy = h-1-y, x
			case 7: // transverse (mirror horizontal + rotate 90 CW)
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 270 CW (90 CCW)
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, c)
		}
	}
	return dst
}
