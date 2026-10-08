package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postPurposeMove(t *testing.T, r http.Handler, req purposeMoveRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling purpose move request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/purpose-moves", bytes.NewReader(body)))
	return rec
}

// purposeMoveWorld is a fund holding 500.000 in Kas Utama (cash), an open
// envelope, and 75.000 of Titipan (bank).
type purposeMoveWorld struct {
	setup    setupResponse
	envelope incidentalResponse
	titipan  purposeResponse
}

func newPurposeMoveWorld(t *testing.T, r http.Handler) purposeMoveWorld {
	t.Helper()
	w := purposeMoveWorld{setup: setUpFund(t, r)}
	w.envelope = openIncidentalFor(t, r, "Bereavement", "2026-09-01")

	rec := postPassThroughPurpose(t, r, "Titipan")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&w.titipan); err != nil {
		t.Fatalf("decoding purpose response: %v", err)
	}

	for _, in := range []transactionRequest{
		{AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID, Direction: "in", Amount: 500_000, OccurredOn: "2026-09-01"},
		{AccountID: w.setup.BankAccountID(t), PurposeID: w.titipan.ID, Direction: "in", Amount: 75_000, OccurredOn: "2026-09-01"},
	} {
		if rec := postTransaction(t, r, in); rec.Code != http.StatusCreated {
			t.Fatalf("seeding POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}
	return w
}

func (w purposeMoveWorld) request(t *testing.T, from, to, amount int64) purposeMoveRequest {
	return purposeMoveRequest{
		FromPurposeID: from, ToPurposeID: to, AccountID: w.setup.CashAccountID(t),
		Amount: amount, OccurredOn: "2026-09-10",
	}
}

func TestPostPurposeMovesRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := postPurposeMove(t, testRouter(t), purposeMoveRequest{
		FromPurposeID: 1, ToPurposeID: 2, AccountID: 1, Amount: 1_000, OccurredOn: "2026-09-10",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/purpose-moves before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// The success path: 201, a reclass_purpose transfer, and two legs in
// GET /api/transactions carrying transfer_reason 'allocation', the two
// purpose names, and the one account - while the fund's own pooled rows keep
// summing as before.
func TestPostPurposeMovesPostsAnAllocationPair(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newPurposeMoveWorld(t, r)

	req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 120_000)
	note := "Duka cita"
	req.Note = &note
	rec := postPurposeMove(t, r, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/purpose-moves = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var transfer transferResponse
	if err := json.NewDecoder(rec.Body).Decode(&transfer); err != nil {
		t.Fatalf("decoding transfer response: %v", err)
	}
	if transfer.Kind != "reclass_purpose" {
		t.Errorf("Kind = %q, want reclass_purpose", transfer.Kind)
	}

	var legs []transactionResponse
	for _, row := range decodeTransactionsList(t, r) {
		if row.Kind == "transfer" && row.TransferID != nil && *row.TransferID == transfer.ID {
			legs = append(legs, row)
		}
	}
	if len(legs) != 2 {
		t.Fatalf("found %d legs, want 2", len(legs))
	}
	var net int64
	for _, leg := range legs {
		if leg.TransferReason == nil || *leg.TransferReason != "allocation" {
			t.Errorf("%s leg transfer_reason = %v, want \"allocation\"", leg.Direction, leg.TransferReason)
		}
		if leg.TransferCorrectsTransactionID != nil {
			t.Errorf("%s leg corrects transaction %d, want nothing", leg.Direction, *leg.TransferCorrectsTransactionID)
		}
		if leg.TransferFromName == nil || *leg.TransferFromName != "Kas Utama" || leg.TransferToName == nil || *leg.TransferToName != "Bereavement" {
			t.Errorf("%s leg from/to = %v/%v, want Kas Utama/Bereavement", leg.Direction, leg.TransferFromName, leg.TransferToName)
		}
		if leg.AccountID != req.AccountID || leg.Amount != 120_000 || leg.Note == nil || *leg.Note != "Duka cita" {
			t.Errorf("%s leg = account %d amount %d note %v, want account %d, 120000, the note", leg.Direction, leg.AccountID, leg.Amount, leg.Note, req.AccountID)
		}
		if leg.Direction == "in" {
			net += leg.Amount
		} else {
			net -= leg.Amount
		}
	}
	if net != 0 {
		t.Errorf("legs net to %d, want 0", net)
	}
}

// A roll and a between_accounts transfer keep what they were: the roll
// reads 'roll', the transfer reads nothing (ADR-036).
func TestTransactionsCarryTheReasonOfEachKindOfPair(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newPurposeMoveWorld(t, r)

	if rec := postTransfer(t, r, transferRequest{
		PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.CashAccountID(t), ToAccountID: w.setup.BankAccountID(t),
		Amount: 10_000, OccurredOn: "2026-09-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: w.setup.CashAccountID(t), PurposeID: w.envelope.PurposeID, Direction: "in", Amount: 40_000, OccurredOn: "2026-09-03",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := postCloseIncidental(t, r, w.envelope.PurposeID, closeIncidentalRequest{
		AccountID: w.setup.CashAccountID(t), ClosedOn: "2026-09-04",
	}); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/incidentals/{id}/close = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var between, roll int
	for _, row := range decodeTransactionsList(t, r) {
		if row.Kind != "transfer" || row.TransferKind == nil {
			continue
		}
		switch *row.TransferKind {
		case "between_accounts":
			between++
			if row.TransferReason != nil {
				t.Errorf("between_accounts leg transfer_reason = %q, want null", *row.TransferReason)
			}
		case "reclass_purpose":
			roll++
			if row.TransferReason == nil || *row.TransferReason != "roll" {
				t.Errorf("roll leg transfer_reason = %v, want \"roll\"", row.TransferReason)
			}
		}
	}
	if between != 2 || roll != 2 {
		t.Errorf("found %d between_accounts legs and %d roll legs, want 2 and 2", between, roll)
	}
}

// Every refusal's status and code. The ledger's own tests cover why; this
// pins what the client is told.
func TestPostPurposeMovesRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		build  func(t *testing.T, r http.Handler, w purposeMoveWorld) purposeMoveRequest
		status int
		code   string
	}{
		{"zero amount", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 0)
		}, http.StatusBadRequest, "invalid_argument"},
		{"negative amount", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, -5)
		}, http.StatusBadRequest, "invalid_argument"},
		{"bad date", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 1_000)
			req.OccurredOn = "10-09-2026"
			return req
		}, http.StatusBadRequest, "invalid_argument"},
		{"same purpose", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, w.setup.MainPurposeID, 1_000)
		}, http.StatusBadRequest, "purpose_move_same_purpose"},
		{"unknown purpose", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, 999_999, 1_000)
		}, http.StatusBadRequest, "invalid_argument"},
		{"unknown account", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 1_000)
			req.AccountID = 999_999
			return req
		}, http.StatusBadRequest, "invalid_argument"},
		{"pass-through as source", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.titipan.ID, w.setup.MainPurposeID, 1_000)
		}, http.StatusConflict, "purpose_move_pass_through"},
		{"pass-through as target", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, w.titipan.ID, 1_000)
		}, http.StatusConflict, "purpose_move_pass_through"},
		{"closed envelope", func(t *testing.T, r http.Handler, w purposeMoveWorld) purposeMoveRequest {
			if rec := postCloseIncidental(t, r, w.envelope.PurposeID, closeIncidentalRequest{
				AccountID: w.setup.CashAccountID(t), ClosedOn: "2026-09-05",
			}); rec.Code != http.StatusOK {
				t.Fatalf("closing the envelope = %d (body: %s)", rec.Code, rec.Body.String())
			}
			return w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 1_000)
		}, http.StatusConflict, "purpose_move_closed"},
		{"more than the source holds", func(t *testing.T, _ http.Handler, w purposeMoveWorld) purposeMoveRequest {
			return w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 500_001)
		}, http.StatusConflict, "purpose_move_insufficient"},
		{"inactive account", func(t *testing.T, r http.Handler, w purposeMoveWorld) purposeMoveRequest {
			// Empty it first: a location holding money cannot be retired (#474).
			if rec := postTransfer(t, r, transferRequest{
				PurposeID: w.titipan.ID, FromAccountID: w.setup.BankAccountID(t), ToAccountID: w.setup.CashAccountID(t),
				Amount: 75_000, OccurredOn: "2026-09-04",
			}); rec.Code != http.StatusCreated {
				t.Fatalf("emptying the bank = %d (body: %s)", rec.Code, rec.Body.String())
			}
			if rec := patchAccount(t, r, w.setup.BankAccountID(t), `{"inactive_on":"2026-09-05"}`); rec.Code != http.StatusOK {
				t.Fatalf("retiring the account = %d (body: %s)", rec.Code, rec.Body.String())
			}
			req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 1_000)
			req.AccountID = w.setup.BankAccountID(t)
			return req
		}, http.StatusConflict, "purpose_move_account_inactive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := testRouter(t)
			w := newPurposeMoveWorld(t, r)
			req := tc.build(t, r, w)

			rows := len(decodeTransactionsList(t, r))
			rec := postPurposeMove(t, r, req)
			if rec.Code != tc.status {
				t.Fatalf("POST /api/purpose-moves = %d, want %d (body: %s)", rec.Code, tc.status, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != tc.code {
				t.Errorf("error code = %q, want %q", got.Code, tc.code)
			}
			if got := len(decodeTransactionsList(t, r)); got != rows {
				t.Errorf("a refused move changed the ledger: %d rows -> %d", rows, got)
			}
		})
	}
}

// Another fund's purpose and account answer like ones that do not exist.
func TestPostPurposeMovesRefusesAnotherFundsRows(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	w := newPurposeMoveWorld(t, r)
	other := setUpOtherFund(t, sqlDB)

	req := w.request(t, w.setup.MainPurposeID, w.envelope.PurposeID, 1_000)
	req.AccountID = other.accountID
	rec := postPurposeMove(t, r, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/purpose-moves with another fund's account = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
