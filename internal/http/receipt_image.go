package http

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder with image.Decode (blank import, decode-only)
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder with image.Decode (blank import, decode-only - there is no Go WebP encoder in this module, and receipts are always re-encoded as JPEG below)
)

// maxReceiptLongEdge and receiptJPEGQuality are the maintainer's own ruling
// (#153, ADR-011 "downscale on upload"): resize so the long edge is at most
// 1600px, never upscale, and always re-encode as JPEG at roughly quality 80.
// A phone photo is typically 3000-4000px on its long edge and multiple
// megabytes; a receipt only needs to be legible, not archival, and PRD
// section 7.4 calls the photo optional evidence, not a primary record.
const (
	maxReceiptLongEdge = 1600
	receiptJPEGQuality = 80
)

// maxReceiptPixels bounds what a decoder may allocate. The 10 MB body cap
// bounds the *compressed* bytes only: a ~100-byte PNG can declare 65000x65000
// in its header and make image/png allocate gigabytes before it notices the
// pixel data is missing (x/image/webp has the same gap). 50 MP clears a 48 MP
// phone sensor with room to spare and keeps one decode to a few hundred MB.
const maxReceiptPixels = 50_000_000

// errReceiptUnsupportedMediaType and errReceiptHEICUnsupported are the two
// distinct rejections the maintainer's ruling asks for: any format this
// package cannot decode at all, and HEIC/HEIF specifically, which needs its
// own clear error rather than surfacing as a generic "unsupported type" - a
// phone that defaults to HEIC is the single most common reason an upload
// would hit this path at all, so the treasurer needs to know to re-export as
// JPEG, not just that "something" was wrong.
var (
	errReceiptUnsupportedMediaType = errors.New("unsupported receipt media type")
	errReceiptHEICUnsupported      = errors.New("HEIC/HEIF receipts are not supported")
	errReceiptTooManyPixels        = errors.New("receipt image dimensions too large")
)

// processReceiptImage turns an uploaded file's raw bytes into what actually
// gets written to disk: EXIF-corrected, downscaled if needed, and always
// re-encoded as JPEG.
//
// Re-encoding unconditionally - even a file already within the size and
// format limits - is deliberate, not incidental: it is also what strips
// EXIF/GPS metadata a phone embeds by default (CLAUDE.md rule 6, data
// minimization - PRD section 6 asks for names, amounts, dates and notes, not
// the GPS coordinates a receipt photo would otherwise carry along for free).
// The orientation tag is read and applied *before* that strip, in
// jpegOrientation below, or the rotation it describes would be lost along
// with the rest of the EXIF block.
func processReceiptImage(raw []byte) ([]byte, error) {
	if isHEIC(raw) {
		return nil, errReceiptHEICUnsupported
	}

	// DecodeConfig reads only the header, so the dimension check costs
	// nothing and runs before any decoder sizes a pixel buffer.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, errReceiptUnsupportedMediaType
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxReceiptPixels {
		return nil, errReceiptTooManyPixels
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errReceiptUnsupportedMediaType
	}

	// Only a JPEG can carry the EXIF orientation tag phones use to record
	// "this sensor was held sideways" instead of rotating the pixels
	// themselves - decoding it through image/jpeg (like every other decoder)
	// silently drops that tag, which is why it has to be read from the raw
	// bytes separately, before the decoded image is used for anything else.
	if format == "jpeg" {
		if orientation := jpegOrientation(raw); orientation > 1 {
			img = applyOrientation(img, orientation)
		}
	}

	return downscaleAndEncodeJPEG(img)
}

// downscaleAndEncodeJPEG resizes img so its long edge is at most
// maxReceiptLongEdge - never upscaling a smaller photo - and always
// re-encodes the result as JPEG, per the maintainer's ruling.
func downscaleAndEncodeJPEG(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	longEdge := w
	if h > longEdge {
		longEdge = h
	}

	if longEdge > maxReceiptLongEdge {
		scale := float64(maxReceiptLongEdge) / float64(longEdge)
		newW := int(math.Round(float64(w) * scale))
		newH := int(math.Round(float64(h) * scale))
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}

		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		// CatmullRom is the maintainer's own pick: a smooth, quality-first
		// resample - appropriate here because this runs once, on upload, not
		// on every request.
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: receiptJPEGQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// isHEIC sniffs for the ISO base media file format "ftyp" box HEIC/HEIF
// photos are wrapped in, and the specific brand codes iPhones (and Android's
// own HEIF support) write - never the filename or the multipart
// Content-Type, both of which the client fully controls and neither of
// which is what actually determines whether image.Decode below can read the
// bytes.
//
// The box layout is: a 4-byte big-endian size, the 4 ASCII bytes "ftyp", then
// a 4-byte major brand. avif/avis (AV1 Image File Format, a sibling
// container the maintainer's ruling does not name) are deliberately left
// out - those fall through to image.Decode, fail to decode, and are reported
// as the generic errReceiptUnsupportedMediaType instead, which is the
// correct rejection for a format this package was never asked to name
// specifically.
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

// jpegOrientation reads the EXIF orientation tag (0x0112) out of raw JPEG
// bytes, or returns 1 (normal - "do nothing") if there is no EXIF block, no
// orientation tag, or anything about the structure fails to parse. A
// malformed or absent EXIF block is not this function's problem to fail on -
// a receipt photo with no orientation tag is not sideways, it just does not
// say so, and the image is still perfectly usable at whatever orientation
// image.Decode already produced.
//
// A small hand-rolled reader rather than an EXIF library (the maintainer's
// own call, #153): the only field this whole feature ever needs is one
// SHORT tag, and a general-purpose EXIF parser is a much larger dependency
// than "find tag 0x0112".
func jpegOrientation(raw []byte) int {
	// JPEG: a stream of markers, each 0xFF followed by a one-byte code. SOI
	// (0xFFD8) has no length or payload; every marker from APP0 (0xFFE0)
	// onward is followed by a 2-byte big-endian length (itself included) and
	// that many bytes of payload. SOS (0xFFDA) is where the actual image
	// data starts - no more markers worth reading appear after it, so
	// scanning stops there rather than continuing into an entropy-coded
	// scan that happens to contain 0xFF bytes of its own.
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
			// Standalone markers with no length/payload of their own.
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

// orientationFromTIFF reads the Orientation tag (0x0112) out of a TIFF
// header + IFD0, the structure every JPEG's EXIF APP1 payload wraps after
// its "Exif\0\0" prefix. Returns 0 if the tag is absent or anything fails to
// parse - jpegOrientation above treats that the same as "no EXIF at all".
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
		// SHORT (type 3): the value occupies the first 2 bytes of the
		// 4-byte value/offset field, never a separate offset elsewhere in
		// the block - a SHORT always fits inline.
		valueField := tiff[start+8 : start+12]
		return int(order.Uint16(valueField[:2]))
	}
	return 0
}

// applyOrientation returns a new image with EXIF orientation o (2-8) applied,
// or img itself unchanged for 1 (normal) or anything outside the valid EXIF
// range. Implemented as an explicit per-orientation pixel remap rather than a
// generic affine transform: eight fixed cases are easier to verify by hand
// (and to unit-test against known-good fixtures) than a matrix multiply that
// would need the same eight cases tested to trust anyway.
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Orientations 5-8 are the ones with a 90-degree rotation, which swap
	// width and height; 2-4 only mirror or rotate 180, keeping the same
	// dimensions.
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
