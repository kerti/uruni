package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// receiptIDsField decodes just the receipt_ids key off a raw JSON body -
// used where the test cares whether the wire actually carries [] rather
// than an absent key or a null (both of which json.Unmarshal into a Go
// []int64 field would silently accept as the same nil value), per #154's
// "always present, [] never null" ruling.
func receiptIDsField(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decoding response body: %v (body: %s)", err, body)
	}
	got, ok := raw["receipt_ids"]
	if !ok {
		t.Fatalf("response has no receipt_ids key (body: %s)", body)
	}
	return got
}

// TestPostTransactionReceiptIDsIsAnEmptyArrayNeverNull is #154's creation
// ruling applied to POST /api/transactions: a row this response just posted
// cannot already have a receipt (a receipt names an existing transaction
// id), so receipt_ids is always [] here - and, critically, the literal JSON
// array "[]", not a null that happens to decode the same way into Go.
func TestPostTransactionReceiptIDsIsAnEmptyArrayNeverNull(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if got := string(receiptIDsField(t, rec.Body.Bytes())); got != "[]" {
		t.Errorf("receipt_ids = %s, want the literal empty array []", got)
	}

	var posted transactionResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&posted); err != nil {
		t.Fatalf("decoding transaction response: %v", err)
	}
	if len(posted.ReceiptIDs) != 0 {
		t.Errorf("receipt_ids = %v, want empty", posted.ReceiptIDs)
	}
}

// TestGetTransactionsReceiptIDsCountsAndOrdersByID is #154's core case: 0, 1
// and 2 receipts across three otherwise-identical rows, each one's
// receipt_ids showing exactly the ids attached to it and no others, oldest
// (lowest id) first.
func TestGetTransactionsReceiptIDsCountsAndOrdersByID(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	zeroID := setUpTransactionForReceipt(t, r, setup)
	oneID := setUpTransactionForReceipt(t, r, setup)
	twoID := setUpTransactionForReceipt(t, r, setup)

	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))

	uploadRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(oneID, 10)+"/receipts", fixture)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", uploadRec.Code, http.StatusCreated, uploadRec.Body.String())
	}
	only := decodeReceiptResponse(t, uploadRec)

	firstRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(twoID, 10)+"/receipts", fixture)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts (1st) = %d, want %d (body: %s)", firstRec.Code, http.StatusCreated, firstRec.Body.String())
	}
	first := decodeReceiptResponse(t, firstRec)

	secondRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(twoID, 10)+"/receipts", fixture)
	if secondRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts (2nd) = %d, want %d (body: %s)", secondRec.Code, http.StatusCreated, secondRec.Body.String())
	}
	second := decodeReceiptResponse(t, secondRec)
	if second.ID <= first.ID {
		t.Fatalf("second receipt id %d, want it greater than the first %d", second.ID, first.ID)
	}

	listRec := getTransactions(t, r)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/transactions = %d, want %d (body: %s)", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	page := decodeTransactionsPage(t, listRec)

	zeroRow := findTransactionRow(t, page, zeroID)
	if len(zeroRow.ReceiptIDs) != 0 {
		t.Errorf("zero-receipt row's receipt_ids = %v, want empty", zeroRow.ReceiptIDs)
	}

	oneRow := findTransactionRow(t, page, oneID)
	if want := []int64{only.ID}; !slices.Equal(oneRow.ReceiptIDs, want) {
		t.Errorf("one-receipt row's receipt_ids = %v, want %v", oneRow.ReceiptIDs, want)
	}

	twoRow := findTransactionRow(t, page, twoID)
	if want := []int64{first.ID, second.ID}; !slices.Equal(twoRow.ReceiptIDs, want) {
		t.Errorf("two-receipt row's receipt_ids = %v, want %v ordered oldest first", twoRow.ReceiptIDs, want)
	}
}

// TestGetTransactionsReceiptIDsNeverCrossesFunds is ListReceiptIDsByTransactionIDs'
// own fund-scoping proof, the same shape TestReceiptRoutesOnAnotherFundsRowAre404
// already established for the single-row GetReceiptForFund: a call naming a
// real receipt id and a real transaction id, both from a different fund,
// must come back empty rather than trusting the id list alone - an id names
// a row, not permission to see it.
func TestGetTransactionsReceiptIDsNeverCrossesFunds(t *testing.T) {
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setup := setUpFund(t, r)

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
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: otherFund.ID, TransactionID: &otherTxn.ID, Path: "does-not-matter.jpg", UploadedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReceipt(other) = %v, want no error", err)
	}

	// Naming our fund's id but the other fund's real transaction id must
	// come back empty - the fund_id half of the WHERE clause is what
	// refuses it, since the id itself does exist in the receipt table.
	rows, err := q.ListReceiptIDsByTransactionIDs(ctx, store.ListReceiptIDsByTransactionIDsParams{
		FundID:         setup.Fund.ID,
		TransactionIds: []*int64{&otherTxn.ID},
	})
	if err != nil {
		t.Fatalf("ListReceiptIDsByTransactionIDs = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want none for another fund's transaction id", rows)
	}

	// End-to-end: our own fund's list must never mention the other fund's
	// receipt id anywhere, even though nothing about the query result size
	// alone would prove that.
	txnID := setUpTransactionForReceipt(t, r, setup)
	listRec := getTransactions(t, r)
	page := decodeTransactionsPage(t, listRec)
	row := findTransactionRow(t, page, txnID)
	if len(row.ReceiptIDs) != 0 {
		t.Errorf("our own untouched row's receipt_ids = %v, want empty", row.ReceiptIDs)
	}
}

// TestGetTransactionsTransferLegsReceiptIDsAreIndependent is the transfer
// case the maintainer's brief called out by name: a transfer's two legs are
// two distinct transaction rows, so a receipt attached to one leg must never
// appear on the other - there is no single "the transfer's receipt."
func TestGetTransactionsTransferLegsReceiptIDsAreIndependent(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)

	depositRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 500_000, OccurredOn: "2026-08-10",
	})
	if depositRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", depositRec.Code, http.StatusCreated, depositRec.Body.String())
	}

	transferRec := postTransfer(t, r, transferRequest{
		PurposeID:     setup.MainPurposeID,
		FromAccountID: setup.CashAccountID(t),
		ToAccountID:   setup.BankAccountID(t),
		Amount:        300_000,
		OccurredOn:    "2026-08-12",
	})
	if transferRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d, want %d (body: %s)", transferRec.Code, http.StatusCreated, transferRec.Body.String())
	}
	var transfer transferResponse
	if err := json.NewDecoder(transferRec.Body).Decode(&transfer); err != nil {
		t.Fatalf("decoding transfer response: %v", err)
	}

	page := decodeTransactionsPage(t, getTransactions(t, r))
	var outLeg, inLeg transactionResponse
	for _, row := range page.Transactions {
		if row.TransferID != nil && *row.TransferID == transfer.ID {
			if row.Direction == "out" {
				outLeg = row
			} else {
				inLeg = row
			}
		}
	}
	if outLeg.ID == 0 || inLeg.ID == 0 {
		t.Fatalf("could not find both transfer legs in %+v", page.Transactions)
	}

	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))
	uploadRec := postReceiptFile(t, r, "/api/transactions/"+strconv.FormatInt(outLeg.ID, 10)+"/receipts", fixture)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts on out leg = %d, want %d (body: %s)", uploadRec.Code, http.StatusCreated, uploadRec.Body.String())
	}
	receipt := decodeReceiptResponse(t, uploadRec)

	page = decodeTransactionsPage(t, getTransactions(t, r))
	outRow := findTransactionRow(t, page, outLeg.ID)
	inRow := findTransactionRow(t, page, inLeg.ID)

	if want := []int64{receipt.ID}; !slices.Equal(outRow.ReceiptIDs, want) {
		t.Errorf("out leg receipt_ids = %v, want %v", outRow.ReceiptIDs, want)
	}
	if len(inRow.ReceiptIDs) != 0 {
		t.Errorf("in leg receipt_ids = %v, want empty - the receipt was only ever attached to the out leg", inRow.ReceiptIDs)
	}
}

// TestSettleReimbursementReceiptIDsIsAnEmptyArray is the settlement route's
// own creation case: the posted kind='reimbursement' row is brand new, so it
// carries the same [] the plain POST /api/transactions case does.
func TestSettleReimbursementReceiptIDsIsAnEmptyArray(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Budi")
	claim := claimFor(t, r, setup, memberID)

	rec := postSettlement(t, r, claim.ID, settleReimbursementRequest{
		AccountID: setup.CashAccountID(t), OccurredOn: "2026-08-20",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../settle = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := string(receiptIDsField(t, rec.Body.Bytes())); got != "[]" {
		t.Errorf("receipt_ids = %s, want the literal empty array []", got)
	}
}

// TestReimbursementReceiptIDsCreatedEmptyListedAccuratelyAndSurvivesPatch
// walks #154's full reimbursement path: [] at creation, a real batched count
// once GET /api/reimbursements lists an existing claim with a photo already
// on it, and - the case a naive "PATCH always resets to []" implementation
// would get wrong - still accurate after a correction that touches only the
// claim's note, because the receipt was never part of what changed.
func TestReimbursementReceiptIDsCreatedEmptyListedAccuratelyAndSurvivesPatch(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Budi")

	createRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID,
		Amount: 30_000, IncurredOn: "2026-08-01",
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	if got := string(receiptIDsField(t, createRec.Body.Bytes())); got != "[]" {
		t.Errorf("receipt_ids at creation = %s, want the literal empty array []", got)
	}
	claim := decodeReimbursement(t, createRec)

	fixture := encodeTestJPEG(t, solidBlockImage(16, 16, 4, 4, fixtureBG, fixtureBlock))
	uploadRec := postReceiptFile(t, r, "/api/reimbursements/"+strconv.FormatInt(claim.ID, 10)+"/receipts", fixture)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST .../receipts = %d, want %d (body: %s)", uploadRec.Code, http.StatusCreated, uploadRec.Body.String())
	}
	receipt := decodeReceiptResponse(t, uploadRec)

	listRec := getReimbursements(t, r, "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/reimbursements = %d, want %d (body: %s)", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var found *reimbursementResponse
	for _, row := range decodeReimbursementsPage(t, listRec).Reimbursements {
		if row.ID == claim.ID {
			row := row
			found = &row
		}
	}
	if found == nil {
		t.Fatalf("claim %d not found in GET /api/reimbursements", claim.ID)
	}
	if want := []int64{receipt.ID}; !slices.Equal(found.ReceiptIDs, want) {
		t.Errorf("listed claim's receipt_ids = %v, want %v", found.ReceiptIDs, want)
	}

	patchRec := patchReimbursement(t, r, claim.ID, `{"note":"receipt was already attached before this correction"}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/reimbursements/%d = %d, want %d (body: %s)", claim.ID, patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	patched := decodeReimbursement(t, patchRec)
	if want := []int64{receipt.ID}; !slices.Equal(patched.ReceiptIDs, want) {
		t.Errorf("patched claim's receipt_ids = %v, want %v (a correction must not lose a receipt uploaded before it)", patched.ReceiptIDs, want)
	}
}
