package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

// The month-as-a-walk claim (ADR-038) for one location (#784): under Riwayat's
// Lokasi filter the month starts on the location's balance, ends on its
// balance, and every row that moved it lands on one line. A Pindah lokasi, which
// the whole fund's walk cannot see, is the location's Moved line: into it plus,
// out of it minus.

// locationWalk is newWalkScenario plus the shapes it lacks for a location: a
// Pindah lokasi in the other direction, one that touches a third location, a pos
// correction made on Bank only, and a Pindah lokasi in the running month.
type locationWalk struct {
	walkScenario
	cashID, bankID, bankBID int64
}

func newLocationWalk(t *testing.T, l *Ledger) locationWalk {
	t.Helper()
	ctx := context.Background()
	s := locationWalk{walkScenario: newWalkScenario(t, l)}
	for name, dst := range map[string]*int64{"Tunai": &s.cashID, "Bank": &s.bankID, "Bank B": &s.bankBID} {
		if err := l.db.QueryRowContext(ctx, `SELECT id FROM account WHERE fund_id = ? AND name = ?`, s.fundID, name).Scan(dst); err != nil {
			t.Fatalf("finding %s: %v", name, err)
		}
	}
	move := func(purposeID, from, to int64, amount money.Amount, date string) {
		t.Helper()
		if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
			FundID: s.fundID, PurposeID: purposeID, FromAccountID: from, ToAccountID: to, Amount: amount, OccurredOn: date,
		}); err != nil {
			t.Fatalf("PostTransferBetweenAccounts(%d -> %d on %s) = %v, want no error", from, to, date, err)
		}
	}
	// Bank back to Tunai in August (June's went the other way), Tunai to Bank B in
	// September, Titipan from Tunai to Bank in October (the running month).
	move(s.mainID, s.bankID, s.cashID, 20_000, "2026-08-22")
	move(s.mainID, s.cashID, s.bankBID, 15_000, "2026-09-08")
	move(s.passID, s.cashID, s.bankID, 5_000, "2026-10-01")

	// A Titipan row on Bank, tagged to Kas Utama afterwards: a pos correction
	// whose two legs are both on Bank.
	titipanOnBank := postEntry(t, l, s.fundID, s.bankID, s.passID, "in", 9_000, "2026-09-15", nil)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: s.fundID, TransactionID: titipanOnBank.ID, PurposeID: s.mainID}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}
	return s
}

// locationFoots is Start + Openings + In - Out + Adjustments + Moved, checked.
func locationFoots(t *testing.T, w ReportWalk) money.Amount {
	t.Helper()
	sum, err := walkFoots(t, w).Add(w.Moved)
	if err != nil {
		t.Fatalf("+ Moved: %v", err)
	}
	return sum
}

// ledgerSumForAccount is the account's balance through a day by raw SQL ("" for
// every row), narrowed to a pos when purposeID is non-zero.
func ledgerSumForAccount(t *testing.T, l *Ledger, fundID, accountID, purposeID int64, through string) money.Amount {
	t.Helper()
	var sum int64
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)
		FROM "transaction"
		WHERE fund_id = ? AND account_id = ? AND (? = 0 OR purpose_id = ?) AND (? = '' OR occurred_on <= ?)`,
		fundID, accountID, purposeID, purposeID, through, through).Scan(&sum)
	if err != nil {
		t.Fatalf("summing account %d through %q = %v, want no error", accountID, through, err)
	}
	return money.FromDB(sum)
}

var locationMonths = []string{"2026-05", "2026-06", "2026-07", "2026-08", "2026-09", "2026-10"}

func TestMonthWalkFootsEachLocationEveryMonthWithAndWithoutAPos(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)
	at := l.at(reportNow)

	locations := map[string]int64{"Tunai": s.cashID, "Bank": s.bankID, "Bank B": s.bankBID}
	pos := map[string]int64{"every pos": 0, "Kas Utama": s.mainID, "Titipan": s.passID, "Sunatan": s.sunatanID, "Arisan": s.arisanID}

	for locName, accountID := range locations {
		for posName, purposeID := range pos {
			t.Run(locName+"/"+posName, func(t *testing.T) {
				var previousEnd money.Amount
				for i, month := range locationMonths {
					p := ReportParams{FundID: s.fundID, Month: month, AccountID: int64Ptr(accountID)}
					if purposeID != 0 {
						p.PurposeID = int64Ptr(purposeID)
					}
					w, err := at.MonthWalk(ctx, p)
					if err != nil {
						t.Fatalf("MonthWalk(%s) = %v, want no error", month, err)
					}
					if !w.Full {
						t.Errorf("%s: Full = false, want a walk for one location", month)
					}
					if got := locationFoots(t, w); got != w.End {
						t.Errorf("%s: Start + Openings + In - Out + Adjustments + Moved = %d, End = %d (%+v)", month, got, w.End, w)
					}
					if i > 0 && w.Start != previousEnd {
						t.Errorf("%s: Start = %d, want the previous month's End %d", month, w.Start, previousEnd)
					}
					previousEnd = w.End

					// Both ends are the ledger's own sums, by a route of their own. The
					// running month's bound is "no bound": a row dated after today counts.
					wantEnd := ledgerSumForAccount(t, l, s.fundID, accountID, purposeID, w.EndOn)
					if month == "2026-10" {
						wantEnd = ledgerSumForAccount(t, l, s.fundID, accountID, purposeID, "")
					}
					if w.End != wantEnd {
						t.Errorf("%s: End = %d, but the ledger sums to %d", month, w.End, wantEnd)
					}
					if start := ledgerSumForAccount(t, l, s.fundID, accountID, purposeID, w.StartOn); w.Start != start {
						t.Errorf("%s: Start = %d, but the ledger through %s sums to %d", month, w.Start, w.StartOn, start)
					}
				}
			})
		}
	}
}

// The location's walk is the fund's walk cut by location: across every location
// the lines add up to the fund's, and the Pindah lokasi, which the fund's walk
// cannot see, cancel to nothing.
func TestMonthWalkLocationsAddUpToTheFundsWalk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)
	at := l.at(reportNow)

	for _, month := range locationMonths {
		fund, err := at.MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: month})
		if err != nil {
			t.Fatalf("MonthWalk(%s) = %v, want no error", month, err)
		}
		if fund.Moved != 0 {
			t.Errorf("%s: the fund's Moved = %d, want 0: a move between locations is invisible to it", month, fund.Moved)
		}
		var sum ReportWalk
		for _, accountID := range []int64{s.cashID, s.bankID, s.bankBID} {
			w, err := at.MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: month, AccountID: int64Ptr(accountID)})
			if err != nil {
				t.Fatalf("MonthWalk(%s, %d) = %v, want no error", month, accountID, err)
			}
			sum.Start += w.Start
			sum.Openings += w.Openings
			sum.In += w.In
			sum.Out += w.Out
			sum.Adjustments += w.Adjustments
			sum.Moved += w.Moved
			sum.End += w.End
		}
		want := ReportWalk{Start: fund.Start, Openings: fund.Openings, In: fund.In, Out: fund.Out, Adjustments: fund.Adjustments, Moved: 0, End: fund.End}
		if sum != want {
			t.Errorf("%s: the locations' lines sum to %+v, want the fund's %+v", month, sum, want)
		}
	}
}

// Hand-worked, so the tests above are not the code agreeing with itself.
func TestMonthWalkOneLocationHandWorked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)
	at := l.at(reportNow)

	tests := []struct {
		name      string
		accountID int64
		purposeID int64
		month     string
		want      ReportWalk
	}{
		// Tunai: opens with 500.000; 100.000 and the 25.000 dues in, 40.000 out; the
		// 50.000 Pindah lokasi to Bank leaves it.
		{"Tunai in June, a move out", s.cashID, 0, "2026-06",
			ReportWalk{StartOn: "2026-05-31", EndOn: "2026-06-30", Openings: 500_000, In: 125_000, Out: 40_000, Moved: -50_000, End: 535_000}},
		// Bank: opens with 200.000 and receives the 50.000 and nothing else.
		{"Bank in June, a move in", s.bankID, 0, "2026-06",
			ReportWalk{StartOn: "2026-05-31", EndOn: "2026-06-30", Openings: 200_000, Moved: 50_000, End: 250_000}},
		// August, Bank to Tunai: Bank sends 20.000 back.
		{"Bank in August, a move out", s.bankID, 0, "2026-08",
			ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", Start: 250_000, Moved: -20_000, End: 230_000}},
		// Bank B is born in August with its opening and nothing before it.
		{"Bank B before it existed", s.bankBID, 0, "2026-07",
			ReportWalk{StartOn: "2026-06-30", EndOn: "2026-07-31"}},
		{"Bank B in August, its opening", s.bankBID, 0, "2026-08",
			ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", Openings: 150_000, End: 150_000}},
		// September: Bank B receives 15.000 from Tunai; a location nothing else
		// touched keeps its balance otherwise (its cek kas matched).
		{"Bank B in September, a move in", s.bankBID, 0, "2026-09",
			ReportWalk{StartOn: "2026-08-31", EndOn: "2026-09-30", Start: 150_000, Moved: 15_000, End: 165_000}},
		// A pos correction made on Bank (Titipan 9.000 to Kas Utama) moves Kas Utama
		// and Titipan in Bank, and leaves Bank's total alone.
		{"Bank in September, every pos", s.bankID, 0, "2026-09",
			ReportWalk{StartOn: "2026-08-31", EndOn: "2026-09-30", Start: 230_000, In: 2_500 + 9_000, End: 241_500}},
		{"Bank and Titipan in September, the correction takes it out", s.bankID, s.passID, "2026-09",
			ReportWalk{StartOn: "2026-08-31", EndOn: "2026-09-30", In: 9_000, Moved: -9_000}},
		{"Bank and Kas Utama in September, the correction puts it in", s.bankID, s.mainID, "2026-09",
			ReportWalk{StartOn: "2026-08-31", EndOn: "2026-09-30", Start: 230_000, In: 2_500, Moved: 9_000, End: 241_500}},
		// The same correction is nothing to Tunai.
		{"Tunai and Titipan in August, a correction on Tunai", s.cashID, s.passID, "2026-08",
			ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", In: 60_000, Moved: -8_000, End: 52_000}},
		// A Pindah lokasi of Titipan in the running month moves only Titipan's money.
		{"Tunai and Titipan in October", s.cashID, s.passID, "2026-10",
			ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-02", Start: 52_000, Moved: -5_000, End: 47_000}},
		{"Bank and Kas Utama in October, untouched", s.bankID, s.mainID, "2026-10",
			ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-02", Start: 241_500, End: 241_500}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ReportParams{FundID: s.fundID, Month: tt.month, AccountID: int64Ptr(tt.accountID)}
			if tt.purposeID != 0 {
				p.PurposeID = int64Ptr(tt.purposeID)
			}
			got, err := at.MonthWalk(ctx, p)
			if err != nil {
				t.Fatalf("MonthWalk() = %v, want no error", err)
			}
			want := tt.want
			want.Full = true
			if got != want {
				t.Errorf("Walk = %+v, want %+v", got, want)
			}
			if sum := locationFoots(t, got); sum != got.End {
				t.Errorf("foots to %d, End = %d", sum, got.End)
			}
		})
	}
}

// The running month's End for a location, with no pos, is the balance Beranda
// shows for it.
func TestMonthWalkRunningEndIsTheLocationsBalance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)

	for _, accountID := range []int64{s.cashID, s.bankID, s.bankBID} {
		w, err := l.at(reportNow).MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: "2026-10", AccountID: int64Ptr(accountID)})
		if err != nil {
			t.Fatalf("MonthWalk() = %v, want no error", err)
		}
		balance, err := l.AccountBalance(ctx, s.fundID, accountID)
		if err != nil {
			t.Fatalf("AccountBalance() = %v, want no error", err)
		}
		if w.End != balance {
			t.Errorf("account %d: End = %d, want AccountBalance %d", accountID, w.End, balance)
		}
	}
}

// A Pindah pos inside one location nets to zero for it: both legs are there. With
// a pos too, it moves that pos's money as it does without a location.
func TestMonthWalkAPindahPosInsideALocationNetsToZeroForIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)
	at := l.at(reportNow)

	// August has a Kas Utama -> Sunatan Pindah pos of 10.000 on Tunai, the envelope's
	// roll back into Kas Utama, and a correction pair (all on Tunai).
	whole, err := at.MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: "2026-08", AccountID: int64Ptr(s.cashID)})
	if err != nil {
		t.Fatalf("MonthWalk() = %v, want no error", err)
	}
	if whole.Moved != 20_000 {
		t.Errorf("Tunai in August: Moved = %d, want +20.000, the Pindah lokasi from Bank alone: the pos moves net to zero in it", whole.Moved)
	}

	// With the pos, Kas Utama in Tunai gives 10.000 to Sunatan and is handed
	// Sunatan's leftover back by the roll; the figures are the pos's own.
	main, err := at.MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: "2026-08", AccountID: int64Ptr(s.cashID), PurposeID: int64Ptr(s.mainID)})
	if err != nil {
		t.Fatalf("MonthWalk() = %v, want no error", err)
	}
	fundMain, err := at.MonthWalk(ctx, ReportParams{FundID: s.fundID, Month: "2026-08", PurposeID: int64Ptr(s.mainID)})
	if err != nil {
		t.Fatalf("MonthWalk() = %v, want no error", err)
	}
	// Every Kas Utama leg of August sits on Tunai except the Pindah lokasi, so
	// Tunai's Kas Utama differs from the pos's own walk by exactly that move.
	if got, want := main.Moved, fundMain.Moved+20_000; got != want {
		t.Errorf("Kas Utama in Tunai: Moved = %d, want the pos's %d plus the 20.000 Pindah lokasi that came in", got, want)
	}
}

// The public report has no location filter; asking it for one is a mistake, not
// a quiet fall-back to the whole fund.
func TestMonthlyReportRefusesALocationFilter(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newLocationWalk(t, l)
	_, err := l.at(reportNow).MonthlyReport(context.Background(), ReportParams{FundID: s.fundID, Month: "2026-09", AccountID: int64Ptr(s.cashID)})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("MonthlyReport(AccountID) = %v, want ErrInvalidArgument", err)
	}
}
