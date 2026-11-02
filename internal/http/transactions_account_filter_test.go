package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// #784: Riwayat's Lokasi filter (?account_id=) on the Transaksi list and on the
// month summary. The filter is applied to every leg first, like the others, so a
// Pindah lokasi shows when either of its legs is in the location, once. These
// tests hold that, the per-leg meaning of "location AND pos", and the summary
// footing for one location against the balance Beranda shows.

// lokasiWorld is oneRowWorld with Titipan also held in cash, so every pos has
// money in both locations and a move of either can run in either direction.
type lokasiWorld struct {
	oneRowWorld
	cash, bank int64
}

func newLokasiWorld(t *testing.T) lokasiWorld {
	t.Helper()
	w := lokasiWorld{oneRowWorld: newOneRowWorld(t)}
	w.cash, w.bank = w.setup.CashAccountID(t), w.setup.BankAccountID(t)
	if rec := postTransaction(t, w.r, transactionRequest{
		AccountID: w.cash, PurposeID: w.titipan.ID, Direction: "in", Amount: 50_000, OccurredOn: "2026-09-02",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seeding POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return w
}

// pindahLokasi posts a Pindah lokasi of purposeID between two locations and
// returns its transfer id.
func (w lokasiWorld) pindahLokasi(t *testing.T, purposeID, from, to, amount int64, date string) int64 {
	t.Helper()
	rec := postTransfer(t, w.r, transferRequest{
		PurposeID: purposeID, FromAccountID: from, ToAccountID: to, Amount: amount, OccurredOn: date,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return decodeTransfer(t, rec).ID
}

func transferIDs(rows []transactionResponse) map[int64]int {
	ids := map[int64]int{}
	for _, row := range rows {
		if row.TransferID != nil {
			ids[*row.TransferID]++
		}
	}
	return ids
}

func TestListAccountFilterShowsAPindahLokasiFromEitherLegOnce(t *testing.T) {
	t.Parallel()
	w := newLokasiWorld(t)
	id := w.pindahLokasi(t, w.setup.MainPurposeID, w.cash, w.bank, 100_000, "2026-09-10")

	cases := []struct {
		name      string
		accountID int64
		wantDir   string // the leg that is the row: the out leg when both match, the matching one otherwise
		wantRows  int    // the location's own rows plus the transfer
	}{
		{"the from location (Tunai)", w.cash, "out", 3},
		{"the to location (Bank)", w.bank, "in", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, _ := w.walk(t, "account_id="+itoa(c.accountID))
			row := assertTransferOnce(t, rows, id)
			if row.Direction != c.wantDir || row.AccountID != c.accountID {
				t.Errorf("representative leg = %s on account %d, want %s on account %d", row.Direction, row.AccountID, c.wantDir, c.accountID)
			}
			// Still a neutral transfer row naming both ends, whichever leg it is.
			if row.TransferFromName == nil || row.TransferToName == nil {
				t.Errorf("transfer names = %v -> %v, want both", row.TransferFromName, row.TransferToName)
			}
			if len(rows) != c.wantRows {
				t.Errorf("rows = %d, want %d", len(rows), c.wantRows)
			}
			for _, r := range rows {
				if r.Kind != "transfer" && r.AccountID != c.accountID {
					t.Errorf("row %d is on account %d under account_id=%d", r.ID, r.AccountID, c.accountID)
				}
			}
		})
	}

	// An id naming no location of this fund matches nothing, as purpose_id does.
	rows, _ := w.walk(t, "account_id=999999")
	if len(rows) != 0 {
		t.Errorf("rows under an unknown account_id = %d, want 0", len(rows))
	}
}

// The pair is chosen after the filters and before the cursor, so a page
// boundary can neither split it nor show it twice under a location filter, from
// either side.
func TestListAccountFilterNeverShowsAPairTwiceAcrossPages(t *testing.T) {
	t.Parallel()
	for after := transactionsPageSize - 2; after <= transactionsPageSize+1; after++ {
		t.Run(fmt.Sprintf("%d rows listed before the pair", after), func(t *testing.T) {
			t.Parallel()
			w := newLokasiWorld(t)
			const date = "2026-09-10"
			w.fill(t, 3, date, w.setup.MainPurposeID)
			id := w.pindahLokasi(t, w.setup.MainPurposeID, w.cash, w.bank, 5_000, date)
			w.fill(t, after, date, w.setup.MainPurposeID)

			for _, accountID := range []int64{w.cash, w.bank} {
				rows, pages := w.walk(t, "account_id="+itoa(accountID))
				assertTransferOnce(t, rows, id)
				if accountID == w.cash && pages < 2 {
					t.Errorf("cash walk took %d page(s), want the boundary crossed", pages)
				}
				for tid, n := range transferIDs(rows) {
					if n != 1 {
						t.Errorf("account %d: transfer %d appears %d times", accountID, tid, n)
					}
				}
			}
		})
	}
}

// Location and pos must hold on the SAME leg. A Pindah lokasi keeps its pos, so
// the pos is on both legs and the location picks the leg; a Pindah pos keeps
// its location, so both legs are there.
func TestListAccountAndPurposeFiltersMeetOnTheSameLeg(t *testing.T) {
	t.Parallel()
	w := newLokasiWorld(t)
	main, titipan := w.setup.MainPurposeID, w.titipan.ID
	kasMove := w.pindahLokasi(t, main, w.cash, w.bank, 100_000, "2026-09-10")
	titipanMove := w.pindahLokasi(t, titipan, w.cash, w.bank, 20_000, "2026-09-11")
	allocation := w.move(t, "2026-09-12", 10_000) // Kas Utama -> envelope, both legs in Tunai

	cases := []struct {
		name      string
		accountID int64
		purposeID int64
		want      []int64
		notWant   []int64
	}{
		{"Tunai and Kas Utama", w.cash, main, []int64{kasMove, allocation}, []int64{titipanMove}},
		{"Tunai and Titipan", w.cash, titipan, []int64{titipanMove}, []int64{kasMove, allocation}},
		{"Bank and Kas Utama", w.bank, main, []int64{kasMove}, []int64{titipanMove, allocation}},
		{"Tunai and the envelope", w.cash, w.envelope.PurposeID, []int64{allocation}, []int64{kasMove, titipanMove}},
		{"Bank and the envelope", w.bank, w.envelope.PurposeID, nil, []int64{kasMove, titipanMove, allocation}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, _ := w.walk(t, "account_id="+itoa(c.accountID)+"&purpose_id="+itoa(c.purposeID))
			ids := transferIDs(rows)
			for _, id := range c.want {
				assertTransferOnce(t, rows, id)
			}
			for _, id := range c.notWant {
				if ids[id] != 0 {
					t.Errorf("transfer %d is listed, want it absent", id)
				}
			}
		})
	}
}

// A pos correction made on Bank moves money in Bank only: both its legs are
// there, so it never shows for Tunai.
func TestListAccountFilterHidesAPosCorrectionMadeOnAnotherLocation(t *testing.T) {
	t.Parallel()
	w := newLokasiWorld(t)
	var titipanRow transactionResponse
	rows, _ := w.walk(t, "account_id="+itoa(w.bank))
	for _, row := range rows {
		if row.PurposeID == w.titipan.ID {
			titipanRow = row
		}
	}
	if titipanRow.ID == 0 {
		t.Fatal("the seeded Titipan row on Bank is not listed")
	}
	rec := postPurposeCorrection(t, w.r, titipanRow.ID, purposeCorrectionRequest{PurposeID: w.setup.MainPurposeID})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST purpose-correction = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if rows, _ := w.walk(t, "account_id="+itoa(w.cash)); countTransferRows(rows) != 0 {
		t.Errorf("Tunai lists %d transfer row(s), want 0: the correction is on Bank", countTransferRows(rows))
	}
	rows, _ = w.walk(t, "account_id="+itoa(w.bank))
	if n := countTransferRows(rows); n != 1 {
		t.Errorf("Bank lists %d transfer row(s), want the correction once", n)
	}
}

func TestListAndSummaryRefuseAMalformedAccountID(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		list := getTransactionsQuery(t, r, "account_id="+bad)
		summary := getTransactionSummary(t, r, "?month=2026-09&account_id="+bad)
		for name, rec := range map[string]*httptest.ResponseRecorder{"list": list, "summary": summary} {
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with account_id=%s = %d, want %d", name, bad, rec.Code, http.StatusBadRequest)
				continue
			}
			if got := decodeError(t, rec); got.Code != "invalid_argument" || got.Field != "account_id" {
				t.Errorf("%s with account_id=%s error = %+v, want invalid_argument naming account_id", name, bad, got)
			}
		}
	}
}

// The month summary under a location walks that location: its ends are its
// balance, a Pindah lokasi is on Moved (in plus, out minus), and the running
// figures meet Beranda's balance for the location.
func TestTransactionSummaryWalksOneLocation(t *testing.T) {
	t.Parallel()
	w := newLokasiWorld(t)
	main, titipan := w.setup.MainPurposeID, w.titipan.ID
	w.pindahLokasi(t, main, w.cash, w.bank, 100_000, "2026-09-10")
	w.pindahLokasi(t, titipan, w.cash, w.bank, 20_000, "2026-09-11")
	w.pindahLokasi(t, main, w.bank, w.cash, 30_000, "2026-09-12") // the other direction
	w.move(t, "2026-09-13", 10_000)                               // Kas Utama -> envelope inside Tunai

	cases := []struct {
		name  string
		query string
		want  transactionSummaryResponse
	}{
		{"Tunai", "month=2026-09&account_id=" + itoa(w.cash),
			transactionSummaryResponse{Full: true, In: 550_000, Moved: -90_000, End: 460_000}},
		{"Bank", "month=2026-09&account_id=" + itoa(w.bank),
			transactionSummaryResponse{Full: true, In: 75_000, Moved: 90_000, End: 165_000}},
		{"Tunai and Kas Utama", "month=2026-09&account_id=" + itoa(w.cash) + "&purpose_id=" + itoa(main),
			transactionSummaryResponse{Full: true, In: 500_000, Moved: -80_000, End: 420_000}},
		{"Bank and Titipan", "month=2026-09&account_id=" + itoa(w.bank) + "&purpose_id=" + itoa(titipan),
			transactionSummaryResponse{Full: true, In: 75_000, Moved: 20_000, End: 95_000}},
		{"a quiet month before", "month=2026-08&account_id=" + itoa(w.cash), transactionSummaryResponse{Full: true}},
		{"a quiet month after", "month=2026-10&account_id=" + itoa(w.cash),
			transactionSummaryResponse{Full: true, Start: 460_000, End: 460_000}},
		{"a location with nothing", "month=2026-09&account_id=999999", transactionSummaryResponse{Full: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeTransactionSummary(t, getTransactionSummary(t, w.r, "?"+c.query))
			got.StartOn, got.EndOn = nil, nil
			if got != c.want {
				t.Errorf("summary = %+v, want %+v", got, c.want)
			}
			// Footing: Start + Openings + In - Out + Adjustments + Moved == End.
			if sum := got.Start + got.Openings + got.In - got.Out + got.Adjustments + got.Moved; sum != got.End {
				t.Errorf("walk foots to %d, End = %d", sum, got.End)
			}
		})
	}

	// The location's End is the balance Beranda shows for it.
	balances := decodeBalances(t, getBalances(t, w.r))
	for _, a := range []struct {
		id   int64
		want int64
	}{{w.cash, 460_000}, {w.bank, 165_000}} {
		if got := accountBalanceFor(t, balances.Accounts, a.id).Balance; got != a.want {
			t.Errorf("GET /api/balances account %d = %d, want %d", a.id, got, a.want)
		}
		got := decodeTransactionSummary(t, getTransactionSummary(t, w.r, "?month=2026-10&account_id="+strconv.FormatInt(a.id, 10)))
		if got.End != a.want {
			t.Errorf("summary End for account %d = %d, want Beranda's %d", a.id, got.End, a.want)
		}
	}
}
