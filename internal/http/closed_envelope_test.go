package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-031 on the wire (#473): once an envelope is closed, filing a claim
// against it, moving a claim onto it, and paying out a claim it already
// held all answer 409 incidental_closed - the same code an ordinary
// posting gets, so the client's copy points at reopening either way.
func TestClosedEnvelopeRefusesClaimsAndPayoutsWith409(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	onEnvelope := createdReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: envelope.PurposeID, Amount: 40_000, IncurredOn: "2026-08-05",
	})
	onMain := createdReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID, Amount: 20_000, IncurredOn: "2026-08-05",
	})
	if rec := postCloseIncidental(t, r, envelope.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID(t), ClosedOn: "2026-08-20",
	}); rec.Code != http.StatusOK {
		t.Fatalf("POST .../close = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	for _, tc := range []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"settling a claim the envelope held", postSettlement(t, r, onEnvelope.ID, settleReimbursementRequest{
			AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-21",
		})},
		{"filing a claim against it", postReimbursement(t, r, reimbursementRequest{
			MemberID: memberID, PurposeID: envelope.PurposeID, Amount: 15_000, IncurredOn: "2026-08-21",
		})},
		{"moving a claim onto it", patchReimbursement(t, r, onMain.ID, fmt.Sprintf(`{"purpose_id":%d}`, envelope.PurposeID))},
	} {
		if tc.rec.Code != http.StatusConflict {
			t.Errorf("%s = %d, want %d (body: %s)", tc.name, tc.rec.Code, http.StatusConflict, tc.rec.Body.String())
			continue
		}
		if code := decodeError(t, tc.rec).Code; code != "incidental_closed" {
			t.Errorf("%s: error code = %q, want %q", tc.name, code, "incidental_closed")
		}
	}
}

// The server owns a dues payment's purpose (#473): a purpose_id on the wire,
// from an older client or a hand-rolled request, is not read - the rows
// land on Kas Utama regardless.
func TestPostDuesPaymentsIgnoresAPurposeOnTheWire(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Jane")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	body := fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"member_id":%d,"occurred_on":"2026-08-12","periods":[{"dues_period":"2026-08","amount":25000}]}`,
		setup.CashAccountID(t), envelope.PurposeID, memberID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dues-payments", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted []transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(posted) != 1 || posted[0].PurposeID != setup.MainPurposeID {
		t.Errorf("posted = %+v, want one row on Kas Utama %d", posted, setup.MainPurposeID)
	}
}

func createdReimbursement(t *testing.T, r http.Handler, req reimbursementRequest) reimbursementResponse {
	t.Helper()
	rec := postReimbursement(t, r, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	return claim
}
