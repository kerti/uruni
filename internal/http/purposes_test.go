package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getPurposes(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/purposes", nil))
	return rec
}

func postPassThroughPurpose(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(passThroughPurposeRequest{Name: name})
	if err != nil {
		t.Fatalf("marshaling pass-through purpose request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pass-through-purposes", bytes.NewReader(body)))
	return rec
}

func TestGetPurposesRequiresAFund(t *testing.T) {
	rec := getPurposes(t, testRouter(t))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/purposes before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostPassThroughPurposesRequiresAFund(t *testing.T) {
	rec := postPassThroughPurpose(t, testRouter(t), "Sumbangan duka")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/pass-through-purposes before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetPurposesReturnsTheMainPurposeSetupCreated(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := getPurposes(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/purposes = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("purposes = %d, want 1 (body: %s)", len(got), rec.Body.String())
	}
	if got[0].Kind != "main" {
		t.Errorf("purpose.kind = %q, want %q", got[0].Kind, "main")
	}
}

func TestPostPassThroughPurposesCreatesAndListReturnsItAlongsideMain(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := postPassThroughPurpose(t, r, "Sumbangan duka")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("purpose.id is zero")
	}
	if created.Name != "Sumbangan duka" {
		t.Errorf("purpose.name = %q, want %q", created.Name, "Sumbangan duka")
	}
	if created.Kind != "pass_through" {
		t.Errorf("purpose.kind = %q, want %q", created.Kind, "pass_through")
	}

	list := getPurposes(t, r)
	var got []purposeResponse
	if err := json.NewDecoder(list.Body).Decode(&got); err != nil {
		t.Fatalf("decoding list: %v (body: %s)", err, list.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("purposes = %d, want 2 (body: %s)", len(got), list.Body.String())
	}

	kinds := map[string]bool{}
	for _, p := range got {
		kinds[p.Kind] = true
	}
	if !kinds["main"] || !kinds["pass_through"] {
		t.Errorf("purpose kinds = %v, want both main and pass_through", kinds)
	}
}

// The caller cannot choose the kind, so there is no way to reach
// purpose_single_main through this route: a body naming kind='main' is
// ignored and the row is still created as a pass-through.
func TestPostPassThroughPurposesIgnoresACallerSuppliedKind(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	body := `{"name":"Bukan utama","kind":"main"}`
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pass-through-purposes", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST with a kind in the body = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.Kind != "pass_through" {
		t.Fatalf("purpose.kind = %q, want %q - the caller must not be able to pick", created.Kind, "pass_through")
	}

	// And main is still unique: exactly one, the one setup created.
	list := getPurposes(t, r)
	var got []purposeResponse
	if err := json.NewDecoder(list.Body).Decode(&got); err != nil {
		t.Fatalf("decoding list: %v (body: %s)", err, list.Body.String())
	}
	mains := 0
	for _, p := range got {
		if p.Kind == "main" {
			mains++
		}
	}
	if mains != 1 {
		t.Errorf("purposes with kind=main = %d, want 1", mains)
	}
}

func TestPostPassThroughPurposesRejectsABlankName(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := postPassThroughPurpose(t, r, "   ")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with a blank name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func patchPurpose(t *testing.T, r http.Handler, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/purposes/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte(body))))
	return rec
}

// A purpose's name is a label: a posted transaction references it by id and
// nothing in the ledger reads the text, so a mistyped one is correctable
// exactly like a location's name.
func TestPatchPurposeRenamesAPassThrough(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	createRec := postPassThroughPurpose(t, r, "Kas Bidan")
	var created purposeResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	rec := patchPurpose(t, r, created.ID, `{"name":"Kas Bidang"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/purposes/{id} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Name != "Kas Bidang" {
		t.Errorf("Name = %q, want %q", updated.Name, "Kas Bidang")
	}
	// The kind is not on the wire and cannot move: a caller that could name
	// it could ask for a second 'main'.
	if updated.Kind != "pass_through" {
		t.Errorf("Kind = %q, want %q", updated.Kind, "pass_through")
	}
}

// 'main' is the fund's own system row (purpose_single_main), not a label the
// treasurer typed. 409, not 404 - the row is there and she may be looking
// right at it.
func TestPatchPurposeRefusesTheMainPurpose(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := patchPurpose(t, r, setup.MainPurposeID, `{"name":"Kas Bidang"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PATCH /api/purposes/{main} = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "purpose_not_renameable" {
		t.Errorf("error code = %q, want %q", got.Code, "purpose_not_renameable")
	}
}

func TestPatchPurposeWithoutANameIsRejected(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	createRec := postPassThroughPurpose(t, r, "Kas Bidang")
	var created purposeResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	rec := patchPurpose(t, r, created.ID, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/purposes/{id} {} = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
