package http

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// postReceiptFile is every upload test's shared request builder: a
// multipart/form-data body with one "file" field - the field name both
// upload routes read (receipts.go's readReceiptUpload).
func postReceiptFile(t *testing.T, r http.Handler, path string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "receipt.jpg")
	if err != nil {
		t.Fatalf("creating multipart file field: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("writing multipart file data: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(rec, req)
	return rec
}

// postReceiptWithNoFileField is TestUploadReceiptRequiresAFile's fixture: a
// well-formed multipart body that simply never names the "file" field the
// handler looks for.
func postReceiptWithNoFileField(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("note", "oops, wrong field"); err != nil {
		t.Fatalf("writing multipart field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(rec, req)
	return rec
}

func getReceipt(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/receipts/"+strconv.FormatInt(id, 10), nil))
	return rec
}

func deleteReceipt(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/receipts/"+strconv.FormatInt(id, 10), nil))
	return rec
}

// decodeReceiptResponse is this file's own decode helper, matching
// decodeReimbursement's shape in reimbursements_test.go.
func decodeReceiptResponse(t *testing.T, rec *httptest.ResponseRecorder) receiptResponse {
	t.Helper()
	var got receiptResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding receipt response: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// setUpTransactionForReceipt posts one ordinary transaction and returns its
// id - every upload-to-a-transaction test's shared fixture.
func setUpTransactionForReceipt(t *testing.T, r http.Handler, setup setupResponse) int64 {
	t.Helper()
	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var txn transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&txn); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	return txn.ID
}

// setUpReimbursementForReceipt posts one claim and returns its id - every
// upload-to-a-reimbursement test's shared fixture.
func setUpReimbursementForReceipt(t *testing.T, r http.Handler, setup setupResponse, memberID int64) int64 {
	t.Helper()
	rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 30_000, IncurredOn: "2026-08-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return decodeReimbursement(t, rec).ID
}

// --- image fixtures ---------------------------------------------------------

// solidBlockImage is every image fixture's shared builder: bg everywhere,
// with a solid block of a different color in the top-left corner - a shape
// deliberately simple enough that JPEG's own lossy 8x8-block compression at
// quality 80 (receipt_image.go's own setting) still leaves it clearly
// distinguishable from the background afterward.
func solidBlockImage(w, h, blockW, blockH int, bg, block color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, blockW, blockH), &image.Uniform{C: block}, image.Point{}, draw.Src)
	return img
}

func encodeTestJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encoding fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

// withEXIFOrientation splices a minimal hand-built APP1 Exif segment (a
// TIFF header, one IFD0 entry naming the Orientation tag 0x0112) right after
// jpegBytes' own SOI marker - the same structure jpegOrientation
// (receipt_image.go) reads, built independently here rather than through
// this package's own encoder, so the test does not just check the code
// against itself.
func withEXIFOrientation(t *testing.T, jpegBytes []byte, orientation byte) []byte {
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

// closerToRed reports whether c reads as much closer to fixtureBlock (a
// strong red) than to fixtureBG (white) - the tolerant check every pixel
// assertion below uses instead of exact equality, since re-encoding through
// JPEG at quality 80 (receipt_image.go) never round-trips exact byte values.
func closerToRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	// RGBA() returns 16-bit-scaled channels; red should dominate clearly.
	return r > 40000 && g < 40000 && b < 40000
}

func closerToWhite(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 55000 && g > 55000 && b > 55000
}

// --- tests -------------------------------------------------------------------

func TestUploadTransactionReceiptStoresAndServesIt(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	fixture := encodeTestJPEG(t, solidBlockImage(64, 64, 16, 16, fixtureBG, fixtureBlock))

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeReceiptResponse(t, rec)
	if got.ID == 0 {
		t.Errorf("receipt id = 0, want a real id")
	}
	if got.UploadedAt == 0 {
		t.Errorf("uploaded_at = 0, want a real timestamp")
	}

	getRec := getReceipt(t, r, got.ID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/receipts/%d = %d, want %d", got.ID, getRec.Code, http.StatusOK)
	}
	if ct := getRec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want %q", ct, "image/jpeg")
	}
	if got := getRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if cc := getRec.Header().Get("Cache-Control"); cc == "" || !bytes.Contains([]byte(cc), []byte("private")) {
		t.Errorf("Cache-Control = %q, want a private directive", cc)
	}

	img, _, err := image.Decode(bytes.NewReader(getRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding served receipt: %v", err)
	}
	if !closerToRed(img.At(4, 4)) {
		t.Errorf("served image's top-left block = %v, want it to read as red", img.At(4, 4))
	}
	if !closerToWhite(img.At(40, 40)) {
		t.Errorf("served image's background = %v, want it to read as white", img.At(40, 40))
	}
}

func TestUploadReimbursementReceiptStoresAndServesIt(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	memberRec := postMember(t, r, memberRequest{Name: "Budi"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}

	claimID := setUpReimbursementForReceipt(t, r, setup, member.ID)

	fixture := encodeTestJPEG(t, solidBlockImage(32, 32, 8, 8, fixtureBG, fixtureBlock))

	rec := postReceiptFile(t, r, "/api/reimbursements/"+strconv.FormatInt(claimID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeReceiptResponse(t, rec)

	getRec := getReceipt(t, r, got.ID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/receipts/%d = %d, want %d", got.ID, getRec.Code, http.StatusOK)
	}
}

// TestUploadReceiptDownscalesALargeImage is the maintainer's own ruling
// (#153): long edge capped at 1600px. 3200x1600 is an exact 2x, so the
// result is an exact 1600x800 with no rounding ambiguity to tolerate.
func TestUploadReceiptDownscalesALargeImage(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	fixture := encodeTestJPEG(t, solidBlockImage(3200, 1600, 100, 100, fixtureBG, fixtureBlock))

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeReceiptResponse(t, rec)

	getRec := getReceipt(t, r, got.ID)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(getRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding served receipt config: %v", err)
	}
	if cfg.Width != 1600 || cfg.Height != 800 {
		t.Errorf("served dimensions = %dx%d, want 1600x800", cfg.Width, cfg.Height)
	}
}

// TestUploadReceiptNeverUpscalesASmallImage is the other half of the same
// ruling - "never upscale".
func TestUploadReceiptNeverUpscalesASmallImage(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	fixture := encodeTestJPEG(t, solidBlockImage(400, 300, 40, 40, fixtureBG, fixtureBlock))

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeReceiptResponse(t, rec)

	getRec := getReceipt(t, r, got.ID)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(getRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding served receipt config: %v", err)
	}
	if cfg.Width != 400 || cfg.Height != 300 {
		t.Errorf("served dimensions = %dx%d, want the original 400x300 (no upscale)", cfg.Width, cfg.Height)
	}
}

// TestUploadReceiptAppliesEXIFOrientation is #153's EXIF ruling: a phone's
// orientation tag has to be applied before resizing, or a sideways receipt
// stays sideways. Orientation 6 (rotate 90 CW) is the common case - a phone
// held upright, portrait, whose sensor itself is mounted landscape.
func TestUploadReceiptAppliesEXIFOrientation(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	// A 64x32 landscape source with its marker block in the top-left 16x16
	// corner, tagged orientation=6. Rotating 90 CW maps that corner to the
	// resulting 32-wide x 64-tall image's top-right: dst(x,y) with
	// dx=h-1-y, dy=x for source (x,y) in the block (x<16, y<16) lands at
	// dx in [16,31], dy in [0,15] - the top-right quadrant.
	plain := encodeTestJPEG(t, solidBlockImage(64, 32, 16, 16, fixtureBG, fixtureBlock))
	fixture := withEXIFOrientation(t, plain, 6)

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	got := decodeReceiptResponse(t, rec)

	getRec := getReceipt(t, r, got.ID)
	img, _, err := image.Decode(bytes.NewReader(getRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding served receipt: %v", err)
	}

	b := img.Bounds()
	if b.Dx() != 32 || b.Dy() != 64 {
		t.Fatalf("served dimensions = %dx%d, want 32x64 (rotated 90deg from the 64x32 source)", b.Dx(), b.Dy())
	}
	if !closerToRed(img.At(24, 8)) {
		t.Errorf("top-right quadrant (post-rotation block) = %v, want it to read as red", img.At(24, 8))
	}
	if !closerToWhite(img.At(4, 4)) {
		t.Errorf("top-left quadrant (should be background after rotation) = %v, want it to read as white", img.At(4, 4))
	}
}

func TestUploadReceiptRejectsAnOversizedBody(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	oversized := make([]byte, maxReceiptRequestBytes+1)

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", oversized)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST oversized receipt = %d, want %d (body: %s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "payload_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "payload_too_large")
	}
}

// TestUploadReceiptRejectsHEIC is the maintainer's own ruling: HEIC/HEIF
// gets its own distinct error, detected by sniffing the ISO base media
// "ftyp" box rather than trusting a filename or Content-Type either could
// lie about.
func TestUploadReceiptRejectsHEIC(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	heic := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0x00, 0x00, 0x00, 0x00}

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", heic)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST HEIC receipt = %d, want %d (body: %s)", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "heic_unsupported" {
		t.Errorf("error code = %q, want %q (distinct from the generic unsupported-type rejection)", got.Code, "heic_unsupported")
	}
}

// A tiny PNG whose header declares 65000x65000 must be refused from the header
// alone - decoding it would allocate gigabytes despite the 10 MB body cap.
func TestUploadReceiptRejectsOversizedDimensionsBeforeDecoding(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 65000)
	binary.BigEndian.PutUint32(ihdr[4:8], 65000)
	ihdr[8], ihdr[9] = 8, 2 // 8-bit RGB
	chunk := append([]byte("IHDR"), ihdr...)
	var bomb bytes.Buffer
	bomb.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&bomb, binary.BigEndian, uint32(13)) // IHDR length
	bomb.Write(chunk)
	_ = binary.Write(&bomb, binary.BigEndian, crc32.ChecksumIEEE(chunk))

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", bomb.Bytes())
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST oversized-dimension PNG = %d, want %d (body: %s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "image_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "image_too_large")
	}
}

func TestUploadReceiptRejectsAnUnsupportedType(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	notAnImage := []byte("this is a note, not a photo of a receipt")

	rec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", notAnImage)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST non-image receipt = %d, want %d (body: %s)", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "unsupported_media_type" {
		t.Errorf("error code = %q, want %q", got.Code, "unsupported_media_type")
	}
}

func TestUploadReceiptRequiresAFile(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)

	rec := postReceiptWithNoFileField(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with no file field = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestUploadReceiptOnAMissingParentIs404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)
	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))

	for _, path := range []string{
		"/api/transactions/999999/receipts",
		"/api/reimbursements/999999/receipts",
	} {
		t.Run(path, func(t *testing.T) {
			rec := postReceiptFile(t, r, path, fixture)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("POST %s = %d, want %d (body: %s)", path, rec.Code, http.StatusNotFound, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "not_found" {
				t.Errorf("error code = %q, want %q", got.Code, "not_found")
			}
		})
	}
}

func TestDeleteReceiptRemovesItAndItsBytesBecomeUnreachable(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	txnID := setUpTransactionForReceipt(t, r, setup)
	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))

	uploadRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	receipt := decodeReceiptResponse(t, uploadRec)

	delRec := deleteReceipt(t, r, receipt.ID)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/receipts/%d = %d, want %d (body: %s)", receipt.ID, delRec.Code, http.StatusNoContent, delRec.Body.String())
	}

	getRec := getReceipt(t, r, receipt.ID)
	if getRec.Code != http.StatusNotFound {
		t.Errorf("GET /api/receipts/%d after delete = %d, want %d", receipt.ID, getRec.Code, http.StatusNotFound)
	}

	// Deleting again names a row that is already gone.
	againRec := deleteReceipt(t, r, receipt.ID)
	if againRec.Code != http.StatusNotFound {
		t.Errorf("DELETE /api/receipts/%d twice = %d, want %d", receipt.ID, againRec.Code, http.StatusNotFound)
	}
}

func TestGetAndDeleteReceiptOnAMissingIDAre404(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	if rec := getReceipt(t, r, 999999); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/receipts/999999 = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec := deleteReceipt(t, r, 999999); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE /api/receipts/999999 = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestReceiptRoutesOnAnotherFundsRowAre404 is #188's own regression applied
// to every receipt route: a second fund's transaction, reimbursement and
// receipt, written straight through the store (the API refuses a second
// fund by design), must each read as not-found from our fund's session -
// the exact shape fund_scope_test.go's own table already proves for the
// routes it covers.
func TestReceiptRoutesOnAnotherFundsRowAre404(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	q := store.New(sqlDB)
	ctx := context.Background()
	otherFund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund(other) = %v, want no error", err)
	}
	otherAccount, err := q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: otherFund.ID, Kind: "cash", Name: "Other Cash", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateAccount(other) = %v, want no error", err)
	}
	otherPurpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: otherFund.ID, Kind: "main", Name: "Other Kas Utama", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(other) = %v, want no error", err)
	}
	otherTxn, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: otherFund.ID, AccountID: otherAccount.ID, PurposeID: otherPurpose.ID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-08-01", Kind: "normal", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(other) = %v, want no error", err)
	}
	otherMember, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: otherFund.ID, Name: "Other Member", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(other) = %v, want no error", err)
	}
	otherClaim, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: otherFund.ID, MemberID: otherMember.ID, PurposeID: otherPurpose.ID,
		Amount: 5_000, IncurredOn: "2026-08-01", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement(other) = %v, want no error", err)
	}
	otherReceipt, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: otherFund.ID, ReimbursementID: &otherClaim.ID, Path: "does-not-matter.jpg", UploadedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReceipt(other) = %v, want no error", err)
	}

	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))

	cases := []struct {
		name string
		call func() *httptest.ResponseRecorder
	}{
		{"POST /api/transactions/{other}/receipts", func() *httptest.ResponseRecorder {
			return postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(otherTxn.ID, 10)+"/receipts", fixture)
		}},
		{"POST /api/reimbursements/{other}/receipts", func() *httptest.ResponseRecorder {
			return postReceiptFile(t, r, "/api/reimbursements/"+strconv.FormatInt(otherClaim.ID, 10)+"/receipts", fixture)
		}},
		{"GET /api/receipts/{other}", func() *httptest.ResponseRecorder {
			return getReceipt(t, r, otherReceipt.ID)
		}},
		{"DELETE /api/receipts/{other}", func() *httptest.ResponseRecorder {
			return deleteReceipt(t, r, otherReceipt.ID)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.call()
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s on another fund's row = %d, want %d (body: %s)", tc.name, rec.Code, http.StatusNotFound, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "not_found" {
				t.Errorf("error code = %q, want %q", got.Code, "not_found")
			}
		})
	}
}

// TestGetAndDeleteReceiptRequireSession is the two byte-serving/mutating
// routes' own check, alongside session_gate_test.go's generic sweep: a
// caller with no session at all must not be able to fetch or delete a real
// receipt's bytes, not just a placeholder id.
func TestGetAndDeleteReceiptRequireSession(t *testing.T) {
	sqlDB := testStoreDB(t)
	authed := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, authed)
	txnID := setUpTransactionForReceipt(t, authed, setup)
	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))
	uploadRec := postReceiptFile(t, authed, "/api/transactions/"+strconv.FormatInt(txnID, 10)+"/receipts", fixture)
	receipt := decodeReceiptResponse(t, uploadRec)

	noSession := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), testLogger(), auth.New(sqlDB), "", t.TempDir())

	if rec := getReceipt(t, noSession, receipt.ID); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/receipts/%d with no session = %d, want %d", receipt.ID, rec.Code, http.StatusUnauthorized)
	}
	if rec := deleteReceipt(t, noSession, receipt.ID); rec.Code != http.StatusUnauthorized {
		t.Errorf("DELETE /api/receipts/%d with no session = %d, want %d", receipt.ID, rec.Code, http.StatusUnauthorized)
	}
}
