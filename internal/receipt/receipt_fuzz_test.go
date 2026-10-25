package receipt

import (
	"testing"
)

// FuzzJPEGOrientation (#525): jpegOrientation parses EXIF/TIFF bytes from an
// untrusted upload by hand. Whatever the input, it must not panic and must
// answer with a real EXIF orientation (1-8), where 1 is "do nothing".
func FuzzJPEGOrientation(f *testing.F) {
	plain := encodeTestJPEG(f, solidBlockImage(8, 8, 4, 4, fixtureBG, fixtureBlock))
	f.Add(plain)
	for o := byte(1); o <= 8; o++ {
		f.Add(withEXIFOrientation(f, plain, o))
	}
	f.Add([]byte{})
	f.Add([]byte{0xFF, 0xD8})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if o := jpegOrientation(raw); o < 1 || o > 8 {
			t.Fatalf("jpegOrientation() = %d, want 1-8", o)
		}
	})
}
