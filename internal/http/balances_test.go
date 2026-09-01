package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getBalances(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/balances", nil))
	return rec
}

func decodeBalances(t *testing.T, rec *httptest.ResponseRecorder) balancesResponse {
	t.Helper()
	var got balancesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding balances response: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

func accountBalanceFor(t *testing.T, accounts []accountBalanceResponse, accountID int64) accountBalanceResponse {
	t.Helper()
	for _, a := range accounts {
		if a.ID == accountID {
			return a
		}
	}
	t.Fatalf("no account balance for %d among %+v", accountID, accounts)
	return accountBalanceResponse{}
}

func purposeBalanceFor(t *testing.T, purposes []purposeBalanceResponse, purposeID int64) purposeBalanceResponse {
	t.Helper()
	for _, p := range purposes {
		if p.ID == purposeID {
			return p
		}
	}
	t.Fatalf("no purpose balance for %d among %+v", purposeID, purposes)
	return purposeBalanceResponse{}
}

func TestGetBalancesRequiresAFund(t *testing.T) {
	rec := getBalances(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/balances before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// TestGetBalancesBeforeAnyPostingIsAllZero is the empty-fund case: setup runs,
// nothing has posted yet, and the route still answers with a row for each
// account and purpose setup created - all at 0, not an empty list.
func TestGetBalancesBeforeAnyPostingIsAllZero(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	rec := getBalances(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/balances = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := decodeBalances(t, rec)

	if got.FundTotal != 0 {
		t.Errorf("fund_total = %d, want 0", got.FundTotal)
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("accounts = %+v, want exactly the 2 accounts setup created", got.Accounts)
	}
	cash := accountBalanceFor(t, got.Accounts, setup.CashAccountID)
	if cash.Balance != 0 || cash.Kind != "cash" {
		t.Errorf("cash account = %+v, want balance=0 kind=cash", cash)
	}
	bank := accountBalanceFor(t, got.Accounts, setup.BankAccountID)
	if bank.Balance != 0 || bank.Kind != "bank" {
		t.Errorf("bank account = %+v, want balance=0 kind=bank", bank)
	}

	if len(got.Purposes) != 1 {
		t.Fatalf("purposes = %+v, want exactly the 1 main purpose setup created", got.Purposes)
	}
	main := purposeBalanceFor(t, got.Purposes, setup.MainPurposeID)
	if main.Balance != 0 || main.Kind != "main" {
		t.Errorf("main purpose = %+v, want balance=0 kind=main", main)
	}
}

// TestGetBalancesReflectsEveryMilestoneSlice is the milestone's acceptance
// criterion: one GET reflects every prior M4 slice's postings correctly. It
// seeds an ordinary in and out, a dues payment, a transfer between cash and
// bank, a reimbursement settlement, and an incidental opened, contributed to,
// disbursed from and closed with a leftover that rolls into main - then
// checks the fund total, every account balance and every purpose balance
// against hand-computed integers.
//
// Hand computation (cash starts and ends in rupiah, never floats):
//
//	cash:   +500,000 (in) -50,000 (out) +25,000 (dues) -200,000 (transfer out)
//	        +100,000 (incidental contribution) -40,000 (incidental disbursement)
//	        +60,000/-60,000 (the roll's two legs, both posted through cash,
//	        net zero on this account) = 335,000
//	bank:   +200,000 (transfer in) -30,000 (reimbursement settlement) = 170,000
//	fund:   cash + bank = 505,000
//
//	main purpose:       +500,000 -50,000 +25,000 (dues) -200,000/+200,000
//	                     (transfer's two legs, net zero on purpose) -30,000
//	                     (reimbursement) +60,000 (roll in) = 505,000
//	incidental purpose:  +100,000 -40,000 -60,000 (roll out) = 0
func TestGetBalancesReflectsEveryMilestoneSlice(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	// Ordinary transactions in and out.
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 500_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed in = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		Direction: "out", Amount: 50_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed out = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// A member and a dues payment.
	memberRec := postMember(t, r, memberRequest{Name: "Jane"})
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", memberRec.Code, http.StatusCreated, memberRec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(memberRec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID, MemberID: member.ID,
		OccurredOn: "2026-08-03",
		Periods:    []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed dues payment = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// A transfer between cash and bank.
	if rec := postTransfer(t, r, transferRequest{
		PurposeID: setup.MainPurposeID, FromAccountID: setup.CashAccountID, ToAccountID: setup.BankAccountID,
		Amount: 200_000, OccurredOn: "2026-08-04",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed transfer = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// A reimbursement claim, settled from the bank.
	reimbRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: member.ID, PurposeID: setup.MainPurposeID, Amount: 30_000, IncurredOn: "2026-08-01",
	})
	if reimbRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", reimbRec.Code, http.StatusCreated, reimbRec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(reimbRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding reimbursement response: %v", err)
	}
	if rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.BankAccountID, OccurredOn: "2026-08-05",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("settle reimbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// An incidental envelope: opened, contributed to, disbursed from, then
	// closed - the leftover rolls into main.
	incRec := postIncidental(t, r, openIncidentalRequest{Occasion: "Lebaran", OpenedOn: "2026-08-01"})
	if incRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/incidentals = %d, want %d (body: %s)", incRec.Code, http.StatusCreated, incRec.Body.String())
	}
	incidental := decodeIncidental(t, incRec)
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: incidental.PurposeID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-06",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("incidental contribution = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: incidental.PurposeID,
		Direction: "out", Amount: 40_000, OccurredOn: "2026-08-07",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("incidental disbursement = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postCloseIncidental(t, r, incidental.PurposeID, closeIncidentalRequest{
		AccountID: setup.CashAccountID, ClosedOn: "2026-08-08",
	}); rec.Code != http.StatusOK {
		t.Fatalf("close incidental = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	// The one GET this milestone slice exists to prove.
	rec := getBalances(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/balances = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := decodeBalances(t, rec)

	if got.FundTotal != 505_000 {
		t.Errorf("fund_total = %d, want 505000", got.FundTotal)
	}

	cash := accountBalanceFor(t, got.Accounts, setup.CashAccountID)
	if cash.Balance != 335_000 {
		t.Errorf("cash balance = %d, want 335000", cash.Balance)
	}
	bank := accountBalanceFor(t, got.Accounts, setup.BankAccountID)
	if bank.Balance != 170_000 {
		t.Errorf("bank balance = %d, want 170000", bank.Balance)
	}

	main := purposeBalanceFor(t, got.Purposes, setup.MainPurposeID)
	if main.Balance != 505_000 {
		t.Errorf("main purpose balance = %d, want 505000", main.Balance)
	}
	incBalance := purposeBalanceFor(t, got.Purposes, incidental.PurposeID)
	if incBalance.Balance != 0 {
		t.Errorf("incidental purpose balance = %d, want 0 (fully collected, disbursed, and rolled)", incBalance.Balance)
	}

	// CONSISTENCY CHECK - this arithmetic belongs here, in the test, and
	// nowhere in the handler: the fund total must equal the sum of the
	// account balances the same GET returned.
	var accountSum int64
	for _, a := range got.Accounts {
		accountSum += a.Balance
	}
	if accountSum != got.FundTotal {
		t.Errorf("sum of account balances = %d, want it to equal fund_total = %d", accountSum, got.FundTotal)
	}
}

// TestGetBalancesTransferMovesAccountsLeavesFundTotalUnchanged is the
// transfer property named in the issue, isolated from the milestone fixture
// above: moving money between cash and bank changes both account balances by
// the same amount in opposite directions, while the fund total - derived
// straight from FundBalance, never summed here - does not move at all.
func TestGetBalancesTransferMovesAccountsLeavesFundTotalUnchanged(t *testing.T) {
	r := testRouter(t)
	setup := setUpFundForTransactions(t, r)

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID, PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 300_000, OccurredOn: "2026-08-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed in = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	before := decodeBalances(t, getBalances(t, r))
	beforeCash := accountBalanceFor(t, before.Accounts, setup.CashAccountID).Balance
	beforeBank := accountBalanceFor(t, before.Accounts, setup.BankAccountID).Balance

	if rec := postTransfer(t, r, transferRequest{
		PurposeID: setup.MainPurposeID, FromAccountID: setup.CashAccountID, ToAccountID: setup.BankAccountID,
		Amount: 120_000, OccurredOn: "2026-08-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("transfer = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	after := decodeBalances(t, getBalances(t, r))
	afterCash := accountBalanceFor(t, after.Accounts, setup.CashAccountID).Balance
	afterBank := accountBalanceFor(t, after.Accounts, setup.BankAccountID).Balance

	if afterCash != beforeCash-120_000 {
		t.Errorf("cash after transfer = %d, want %d", afterCash, beforeCash-120_000)
	}
	if afterBank != beforeBank+120_000 {
		t.Errorf("bank after transfer = %d, want %d", afterBank, beforeBank+120_000)
	}
	if after.FundTotal != before.FundTotal {
		t.Errorf("fund_total after transfer = %d, want unchanged at %d", after.FundTotal, before.FundTotal)
	}
}

// TestGetBalancesOnADeadDatabaseIs500 pins down that a dead database produces
// a clean 500 with the generic envelope - not a panic, and not a half-written
// 200 body carrying a fund_total the handler could not finish.
//
// Note what it does NOT reach. resolveFund runs first and queries the same
// database, so any fault wide enough to break FundBalance breaks ListFunds a
// line earlier and returns there. The five `if err != nil` branches below
// resolveFund are unreachable through this route by any whole-database
// failure; distinguishing them would take per-call fault injection, and the
// seam that would need is not worth owning for branches that only ever fire
// together. They stay as honest defensive code, uncovered on purpose.
func TestGetBalancesOnADeadDatabaseIs500(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)

	if rec := postSetup(t, r, "Dana Warga"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Nothing can read after this, so every branch below the first is
	// unreachable by construction - the point is the shape of the answer.
	// That now includes the session manager's own store read (session.go's
	// ErrorFunc): #116 gates this route, so the request below needs its
	// cookie's session loaded before GetBalances ever runs, and a dead
	// database answers that lookup with the identical 500 envelope this
	// test already asserts on.
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing the test database = %v, want no error", err)
	}

	rec := getBalances(t, r)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/balances on a closed database = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "fund_total") {
		t.Errorf("body = %s, want no partial balances payload - the handler must not write before it has every figure", body)
	}
}
