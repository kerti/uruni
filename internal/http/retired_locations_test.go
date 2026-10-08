package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// retiredWorld is a fund with money in it and its bank retired (#444): the
// bank took 200.000 into Kas Utama and moved it to cash (Pindah lokasi)
// before retiring, since a location holding money cannot be retired (#474).
// Cash holds 700.000 of Kas Utama, an open envelope holds 50.000 of
// contributions (cash), one member and one unsettled claim exist. Every
// refusal below must leave those integers where they were.
type retiredWorld struct {
	setup      setupResponse
	envelopeID int64
	memberID   int64
	claimID    int64
}

func newRetiredWorld(t *testing.T, r http.Handler) retiredWorld {
	t.Helper()
	w := retiredWorld{setup: setUpFund(t, r)}
	w.envelopeID = openIncidentalFor(t, r, "Bereavement", "2026-09-01").PurposeID
	w.memberID = memberFor(t, r, "Budi")

	for _, in := range []transactionRequest{
		{AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID, Direction: "in", Amount: 500_000, OccurredOn: "2026-09-01"},
		{AccountID: w.setup.BankAccountID(t), PurposeID: w.setup.MainPurposeID, Direction: "in", Amount: 200_000, OccurredOn: "2026-09-01"},
		{AccountID: w.setup.CashAccountID(t), PurposeID: w.envelopeID, Direction: "in", Amount: 50_000, OccurredOn: "2026-09-02"},
	} {
		if rec := postTransaction(t, r, in); rec.Code != http.StatusCreated {
			t.Fatalf("seeding POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}

	if rec := postTransfer(t, r, transferRequest{
		PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.BankAccountID(t), ToAccountID: w.setup.CashAccountID(t),
		Amount: 200_000, OccurredOn: "2026-09-04",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("emptying the bank: POST /api/transfers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := postReimbursement(t, r, reimbursementRequest{
		MemberID: w.memberID, PurposeID: w.setup.MainPurposeID, Amount: 75_000, IncurredOn: "2026-09-03",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seeding POST /api/reimbursements = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement: %v", err)
	}
	w.claimID = claim.ID

	w.retireBank(t, r)
	return w
}

func (w retiredWorld) retireBank(t *testing.T, r http.Handler) {
	t.Helper()
	if rec := patchAccount(t, r, w.setup.BankAccountID(t), `{"inactive_on":"2026-09-05"}`); rec.Code != http.StatusOK {
		t.Fatalf("retiring the bank = %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func (w retiredWorld) reinstateBank(t *testing.T, r http.Handler) {
	t.Helper()
	if rec := patchAccount(t, r, w.setup.BankAccountID(t), `{"inactive_on":null}`); rec.Code != http.StatusOK {
		t.Fatalf("reinstating the bank = %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// worldState is everything a refused request must leave alone, read back
// through the API.
type worldState struct {
	rows, reconciliations      int
	fund, cash, bank, envelope int64
	envelopeClosed             bool
	claimSettled               bool
}

func (w retiredWorld) state(t *testing.T, r http.Handler) worldState {
	t.Helper()
	var s worldState
	s.rows = len(decodeTransactionsList(t, r))

	var page reconciliationsPageResponse
	if err := json.NewDecoder(getReconciliations(t, r).Body).Decode(&page); err != nil {
		t.Fatalf("decoding reconciliations: %v", err)
	}
	s.reconciliations = len(page.Reconciliations)

	b := decodeBalances(t, getBalances(t, r))
	s.fund = b.FundTotal
	s.cash = accountBalanceFor(t, b.Accounts, w.setup.CashAccountID(t)).Balance
	s.bank = accountBalanceFor(t, b.Accounts, w.setup.BankAccountID(t)).Balance
	for _, p := range b.Purposes {
		if p.ID == w.envelopeID {
			s.envelope = p.Balance
		}
	}

	s.envelopeClosed = decodeIncidental(t, getIncidentalDetail(t, r, w.envelopeID)).ClosedOn != nil

	rec := getReimbursement(t, r, itoa(w.claimID))
	var claim reimbursementResponse
	if err := json.NewDecoder(rec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement %d: %v (body: %s)", w.claimID, err, rec.Body.String())
	}
	s.claimSettled = claim.Settled
	return s
}

func TestRetiredWorldStartsWhereTheTestsExpect(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newRetiredWorld(t, r)
	got := w.state(t, r)
	want := worldState{rows: 5, fund: 750_000, cash: 750_000, bank: 0, envelope: 50_000}
	if got != want {
		t.Fatalf("starting state = %+v, want %+v", got, want)
	}
}

// Every endpoint that posts against a location the treasurer chose answers a
// retired one with 409 account_inactive and writes nothing (#444).
func TestPostingEndpointsRefuseARetiredLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder
	}{
		{"POST /api/transactions in", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransaction(t, r, transactionRequest{
				AccountID: w.setup.BankAccountID(t), PurposeID: w.setup.MainPurposeID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/transactions out", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransaction(t, r, transactionRequest{
				AccountID: w.setup.BankAccountID(t), PurposeID: w.setup.MainPurposeID,
				Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/transactions adjustment", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransaction(t, r, transactionRequest{
				AccountID: w.setup.BankAccountID(t), PurposeID: w.setup.MainPurposeID,
				Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10", IsAdjustment: true,
			})
		}},
		{"POST /api/transactions named contribution", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransaction(t, r, transactionRequest{
				AccountID: w.setup.BankAccountID(t), PurposeID: w.envelopeID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10", MemberID: &w.memberID,
			})
		}},
		{"POST /api/transactions titipan", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			rec := postPassThroughPurpose(t, r, "Titipan")
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/pass-through-purposes = %d (body: %s)", rec.Code, rec.Body.String())
			}
			var titipan purposeResponse
			if err := json.NewDecoder(rec.Body).Decode(&titipan); err != nil {
				t.Fatalf("decoding purpose: %v", err)
			}
			return postTransaction(t, r, transactionRequest{
				AccountID: w.setup.BankAccountID(t), PurposeID: titipan.ID,
				Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/dues-payments", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postDuesPayment(t, r, duesPaymentRequest{
				AccountID: w.setup.BankAccountID(t),
				MemberID:  w.memberID, OccurredOn: "2026-09-10",
				Periods: []duesPaymentPeriod{
					{DuesPeriod: "2026-09", Amount: 25_000},
					{DuesPeriod: "2026-10", Amount: 25_000},
				},
			})
		}},
		{"POST /api/transfers out of it", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransfer(t, r, transferRequest{
				PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.BankAccountID(t),
				ToAccountID: w.setup.CashAccountID(t), Amount: 10_000, OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/transfers into it", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postTransfer(t, r, transferRequest{
				PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.CashAccountID(t),
				ToAccountID: w.setup.BankAccountID(t), Amount: 10_000, OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/reimbursements/{id}/settle", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postSettlement(t, r, w.claimID, settleReimbursementRequest{
				AccountID: w.setup.BankAccountID(t), OccurredOn: "2026-09-10",
			})
		}},
		{"POST /api/incidentals/{id}/close", func(t *testing.T, r http.Handler, w retiredWorld) *httptest.ResponseRecorder {
			return postCloseIncidental(t, r, w.envelopeID, closeIncidentalRequest{
				AccountID: w.setup.BankAccountID(t), ClosedOn: "2026-09-10",
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testRouter(t)
			w := newRetiredWorld(t, r)
			before := w.state(t, r)

			rec := tc.do(t, r, w)
			if rec.Code != http.StatusConflict {
				t.Fatalf("%s to a retired location = %d, want %d (body: %s)", tc.name, rec.Code, http.StatusConflict, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "account_inactive" {
				t.Errorf("error code = %q, want %q", got.Code, "account_inactive")
			}
			if got := w.state(t, r); got != before {
				t.Errorf("a refused request changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

// PostPurposeMove's own code is untouched: the SPA already speaks it.
func TestPurposeMoveKeepsItsOwnRetiredLocationCode(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newRetiredWorld(t, r)

	rec := postPurposeMove(t, r, purposeMoveRequest{
		FromPurposeID: w.setup.MainPurposeID, ToPurposeID: w.envelopeID,
		AccountID: w.setup.BankAccountID(t), Amount: 1_000, OccurredOn: "2026-09-10",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/purpose-moves = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "purpose_move_account_inactive" {
		t.Errorf("error code = %q, want %q", got.Code, "purpose_move_account_inactive")
	}
}

// The controls: the active location still posts beside a retired one, and a
// reinstated one posts again with its balance intact.
func TestPostingEndpointsStillAcceptActiveAndReinstatedLocations(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newRetiredWorld(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: w.setup.CashAccountID(t), PurposeID: w.setup.MainPurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions on active cash = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	w.reinstateBank(t, r)
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: w.setup.BankAccountID(t), PurposeID: w.setup.MainPurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions on the reinstated bank = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := w.state(t, r); got.cash != 760_000 || got.bank != 10_000 || got.fund != 770_000 {
		t.Errorf("cash/bank/fund = %d/%d/%d, want 760000/10000/770000", got.cash, got.bank, got.fund)
	}
}

// Corrections keep their way home: a reversal takes its location from the row
// it reverses, so a dues payment on a since-retired location is still
// reversible (CLAUDE.md rule 3). The payment's money is moved to cash before
// the bank retires (#474), so the reversal leaves the retired bank negative -
// visible on Beranda, and the treasurer's to settle by reinstating it.
func TestReversalOfARowOnARetiredLocationStillWorks(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newRetiredWorld(t, r)
	w.reinstateBank(t, r)

	rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: w.setup.BankAccountID(t),
		MemberID:  w.memberID, OccurredOn: "2026-09-04",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted []transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding dues payment: %v", err)
	}
	if rec := postTransfer(t, r, transferRequest{
		PurposeID: w.setup.MainPurposeID, FromAccountID: w.setup.BankAccountID(t), ToAccountID: w.setup.CashAccountID(t),
		Amount: 25_000, OccurredOn: "2026-09-04",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("emptying the bank = %d (body: %s)", rec.Code, rec.Body.String())
	}
	w.retireBank(t, r)

	rev := postDuesPaymentReversal(t, r, posted[0].ID, reverseDuesPaymentRequest{OccurredOn: "2026-09-12"})
	if rev.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal on a retired location = %d, want %d (body: %s)", rev.Code, http.StatusCreated, rev.Body.String())
	}
	if got := w.state(t, r); got.bank != -25_000 || got.fund != 750_000 {
		t.Errorf("bank/fund = %d/%d, want -25000/750000 after payment, move and reversal", got.bank, got.fund)
	}
}

// --- Cek kas ----------------------------------------------------------------

func TestPostReconciliationsRefusesACountOfARetiredLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		counts func(w retiredWorld, t *testing.T) []accountCountRequest
	}{
		{"both counted, one retired", func(w retiredWorld, t *testing.T) []accountCountRequest {
			return []accountCountRequest{
				{AccountID: w.setup.CashAccountID(t), ActualAmount: 750_000, Resolution: "matched"},
				{AccountID: w.setup.BankAccountID(t), ActualAmount: 0, Resolution: "matched"},
			}
		}},
		{"only the retired one", func(w retiredWorld, t *testing.T) []accountCountRequest {
			return []accountCountRequest{{AccountID: w.setup.BankAccountID(t), ActualAmount: 0, Resolution: "matched"}}
		}},
		{"a fix aimed at the retired one", func(w retiredWorld, t *testing.T) []accountCountRequest {
			return []accountCountRequest{
				{AccountID: w.setup.CashAccountID(t), ActualAmount: 750_000, Resolution: "matched"},
				{
					AccountID: w.setup.BankAccountID(t), ActualAmount: 10_000, Resolution: "adjusted",
					Fix: &fixRequest{PurposeID: w.setup.MainPurposeID, Direction: "in", Amount: 10_000, OccurredOn: "2026-09-10"},
				},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testRouter(t)
			w := newRetiredWorld(t, r)
			before := w.state(t, r)

			rec := postReconciliation(t, r, takeReconciliationRequest{Counts: tc.counts(w, t)})
			if rec.Code != http.StatusConflict {
				t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "account_inactive" {
				t.Errorf("error code = %q, want %q", got.Code, "account_inactive")
			}
			if got := w.state(t, r); got != before {
				t.Errorf("a refused snapshot changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

func TestPostReconciliationsRefusesASnapshotThatOmitsAnActiveLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		counts func(w retiredWorld, t *testing.T) []accountCountRequest
	}{
		{"cash only", func(w retiredWorld, t *testing.T) []accountCountRequest {
			return []accountCountRequest{{AccountID: w.setup.CashAccountID(t), ActualAmount: 750_000, Resolution: "matched"}}
		}},
		{"a fix on the counted line must not post", func(w retiredWorld, t *testing.T) []accountCountRequest {
			return []accountCountRequest{{
				AccountID: w.setup.CashAccountID(t), ActualAmount: 740_000, Resolution: "adjusted",
				Fix: &fixRequest{PurposeID: w.setup.MainPurposeID, Direction: "out", Amount: 10_000, OccurredOn: "2026-09-10"},
			}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testRouter(t)
			w := newRetiredWorld(t, r)
			w.reinstateBank(t, r) // both locations active
			before := w.state(t, r)

			rec := postReconciliation(t, r, takeReconciliationRequest{Counts: tc.counts(w, t)})
			if rec.Code != http.StatusConflict {
				t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "reconciliation_location_missing" {
				t.Errorf("error code = %q, want %q", got.Code, "reconciliation_location_missing")
			}
			if got := w.state(t, r); got != before {
				t.Errorf("a refused snapshot changed the ledger: %+v -> %+v", before, got)
			}
		})
	}
}

// The happy path the two refusals frame: every active location counted, the
// retired one left out.
func TestPostReconciliationsCountsExactlyTheActiveLocations(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	w := newRetiredWorld(t, r)

	rec := postReconciliation(t, r, takeReconciliationRequest{Counts: []accountCountRequest{
		{AccountID: w.setup.CashAccountID(t), ActualAmount: 750_000, Resolution: "matched"},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reconciliations = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	detail := decodeReconciliationDetail(t, rec)
	if len(detail.Lines) != 1 || detail.Lines[0].AccountID != w.setup.CashAccountID(t) || detail.Lines[0].DifferenceAmount != 0 {
		t.Errorf("lines = %+v, want exactly one matched line for cash", detail.Lines)
	}
}
