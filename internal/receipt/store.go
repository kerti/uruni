package receipt

import (
	"crypto/rand"
	"encoding/hex"
	"io"

	"github.com/kerti/uruni/internal/backup"
)

// Store writes data under a fresh, server-generated name inside uploadsDir and
// returns that name (the path CreateReceipt stores, relative to uploadsDir,
// never derived from client input, so no path-traversal surface). It writes a
// temp file in the same directory and renames into place, so a reader never
// sees a partial file under the final name. The HTTP upload and the demo
// seeder both store through it.
func Store(uploadsDir string, data []byte) (string, error) {
	name, err := randomFilename()
	if err != nil {
		return "", err
	}

	// In uploadsDir, not os.TempDir(): os.Rename across filesystems fails, and a
	// container's /uploads volume is often a different filesystem from the OS
	// temp directory. WriteFileAtomic also fsyncs the file before the rename and
	// the directory after, so a power loss cannot leave a receipt row naming an
	// empty file (#501).
	err = backup.WriteFileAtomic(uploadsDir, ".receipt-upload-*.tmp", name, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// randomFilename returns a fresh crypto/rand name, never derived from the
// client's filename (#153): nothing about a stored receipt's name is
// attacker-influenced, so there is no path-traversal surface to sanitize.
// Every receipt is re-encoded as JPEG (Process), so the extension is always
// .jpg.
func randomFilename() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ".jpg", nil
}
