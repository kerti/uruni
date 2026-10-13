package http

import (
	"bytes"
	"context"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// The caller cannot choose the kind, so purpose_single_main is unreachable
// through this route: a body naming kind='main' is ignored and the row is
// created as a pass-through.
func TestPostPassThroughPurposesIgnoresACallerSuppliedKind(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
// nothing in the ledger reads the text, so a mistyped one is correctable like
// a location's name.
func TestPatchPurposeRenamesAPassThrough(t *testing.T) {
	t.Parallel()
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
	// The kind is not on the wire and cannot move: a caller naming it could ask
	// for a second 'main'.
	if updated.Kind != "pass_through" {
		t.Errorf("Kind = %q, want %q", updated.Kind, "pass_through")
	}
}

// 'main' is the fund's system row (purpose_single_main), not a label the
// treasurer typed. 409, not 404: the row is there and she may be looking at
// it. The only kind PATCH refuses (#264); an incidental's occasion is
// renameable alongside a pass-through's name.
func TestPatchPurposeRefusesTheMainPurpose(t *testing.T) {
	t.Parallel()
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

// #264: a mistyped occasion is correctable like a location's name: renaming an
// incidental moves purpose.name and incidental.occasion together, posts no
// transaction and changes no balance. Balance and transaction-count checks
// are read fresh off the ledger, not trusted from the PATCH response, as in
// TestCloseIncidentalRollsLeftoverVerifiedThroughBalances.
func TestPatchPurposeRenamesAnIncidentalsOccasionAndMovesNoMoney(t *testing.T) {
	t.Parallel()
	r, l := testRouterAndLedger(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Halal bihalal RT", "2026-08-01")

	// A contribution, so there is a real fund balance and transaction row to
	// prove untouched.
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	ctx := context.Background()
	fundBefore, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	envelopeBalBefore, err := l.PurposeBalance(ctx, setup.Fund.ID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(envelope) before = %v, want no error", err)
	}
	txBefore := decodeTransactionsPage(t, getTransactions(t, r))

	rec := patchPurpose(t, r, envelope.PurposeID, `{"name":"Halal bihalal RT 2026"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/purposes/{incidental} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Name != "Halal bihalal RT 2026" {
		t.Errorf("Name = %q, want %q", updated.Name, "Halal bihalal RT 2026")
	}
	if updated.Kind != "incidental" {
		t.Errorf("Kind = %q, want %q", updated.Kind, "incidental")
	}

	// incidental.occasion moved with purpose.name: the detail route reads it from
	// the incidental row.
	detailRec := getIncidentalDetail(t, r, envelope.PurposeID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET /api/incidentals/{id} after rename = %d, want %d (body: %s)", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
	detail := decodeIncidental(t, detailRec)
	if detail.Occasion != "Halal bihalal RT 2026" {
		t.Errorf("incidental.Occasion = %q, want %q - both rows must move together", detail.Occasion, "Halal bihalal RT 2026")
	}

	// The trust-core assertion: renaming posts no transaction and changes no
	// balance. Integers compared exactly, no float on this path (ADR-015).
	txAfter := decodeTransactionsPage(t, getTransactions(t, r))
	if len(txAfter.Transactions) != len(txBefore.Transactions) {
		t.Errorf("transaction count after rename = %d, want %d (unchanged) - a rename must post nothing", len(txAfter.Transactions), len(txBefore.Transactions))
	}
	fundAfter, err := l.FundBalance(ctx, setup.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if fundAfter != fundBefore {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a rename moves no money", fundBefore, fundAfter)
	}
	envelopeBalAfter, err := l.PurposeBalance(ctx, setup.Fund.ID, envelope.PurposeID)
	if err != nil {
		t.Fatalf("PurposeBalance(envelope) after = %v, want no error", err)
	}
	if envelopeBalAfter != envelopeBalBefore {
		t.Errorf("PurposeBalance(envelope) before=%d after=%d, want identical - a rename moves no money", envelopeBalBefore, envelopeBalAfter)
	}
}

// A closed envelope is renameable too (#264: the typo is usually found after
// the occasion is over).
func TestPatchPurposeRenamesAClosedIncidental(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Halal bihalal RT", "2026-08-01")

	if rec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-10",
	}); rec.Code != http.StatusOK {
		t.Fatalf("close = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec := patchPurpose(t, r, envelope.PurposeID, `{"name":"Halal bihalal RT (typo fixed)"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/purposes/{closed incidental} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated purposeResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Name != "Halal bihalal RT (typo fixed)" {
		t.Errorf("Name = %q, want %q", updated.Name, "Halal bihalal RT (typo fixed)")
	}
}

// An empty or whitespace-only occasion is refused, the same as opening one.
func TestPatchPurposeRejectsAnEmptyIncidentalOccasion(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Halal bihalal RT", "2026-08-01")

	rec := patchPurpose(t, r, envelope.PurposeID, `{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/purposes/{incidental} with a blank name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchPurposeWithoutANameIsRejected(t *testing.T) {
	t.Parallel()
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

func getSelectablePurposes(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/purposes?selectable=true", nil))
	return rec
}

// GET /api/purposes?selectable=true excludes a closed incidental's purpose
// (ADR-031: the record-transaction picker stops offering what
// PostTransaction's guard refuses), while unfiltered GET /api/purposes returns
// it, a closed envelope being history. purposeResponse gains no closed_on
// either way.
func TestGetSelectablePurposesExcludesAClosedIncidental(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Jane's wedding", "2026-08-01")

	// Open: present on the selectable list too.
	if rec := getSelectablePurposes(t, r); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/purposes?selectable=true (open envelope) = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	} else {
		var got []purposeResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if !containsPurposeID(got, envelope.PurposeID) {
			t.Errorf("selectable purposes = %+v, want the open envelope's purpose %d included", got, envelope.PurposeID)
		}
	}

	if rec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-10",
	}); rec.Code != http.StatusOK {
		t.Fatalf("close = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	selectableRec := getSelectablePurposes(t, r)
	if selectableRec.Code != http.StatusOK {
		t.Fatalf("GET /api/purposes?selectable=true = %d, want %d (body: %s)", selectableRec.Code, http.StatusOK, selectableRec.Body.String())
	}
	var selectable []purposeResponse
	if err := json.NewDecoder(selectableRec.Body).Decode(&selectable); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if containsPurposeID(selectable, envelope.PurposeID) {
		t.Errorf("selectable purposes = %+v, want the closed envelope's purpose %d excluded", selectable, envelope.PurposeID)
	}
	// main is still there - only the closed incidental is filtered.
	hasMain := false
	for _, p := range selectable {
		if p.Kind == "main" {
			hasMain = true
		}
	}
	if !hasMain {
		t.Errorf("selectable purposes = %+v, want the fund's main purpose included", selectable)
	}

	// The unfiltered list returns everything, closed envelope included.
	unfiltered := getPurposes(t, r)
	var got []purposeResponse
	if err := json.NewDecoder(unfiltered.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !containsPurposeID(got, envelope.PurposeID) {
		t.Errorf("GET /api/purposes (unfiltered) = %+v, want the closed envelope's purpose %d still included", got, envelope.PurposeID)
	}
}

func containsPurposeID(purposes []purposeResponse, id int64) bool {
	for _, p := range purposes {
		if p.ID == id {
			return true
		}
	}
	return false
}

// An unparseable ?selectable value is a 400, as listIncidentals' guard on
// ?open.
func TestGetPurposesRejectsAnUnparseableSelectableValue(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/purposes?selectable=maybe", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/purposes?selectable=maybe = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}
