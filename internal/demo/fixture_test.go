package demo

import (
	"context"
	"database/sql"
	"image"
	_ "image/jpeg" // registers the JPEG decoder, the format every receipt is stored in
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// seeded is a demo database written on a fixed "today".
type seeded struct {
	db      *sql.DB
	fundID  int64
	uploads string
	l       *ledger.Ledger // its clock stands on now
	now     time.Time
}

func seedAt(t *testing.T, now time.Time) seeded {
	t.Helper()
	sqlDB := newDB(t)
	uploads := t.TempDir()
	ctx := context.Background()
	if err := Ensure(ctx, sqlDB, uploads, func() time.Time { return now }); err != nil {
		t.Fatalf("Ensure() on %s = %v, want the whole history to replay without a refusal", now.In(wib(t)).Format(time.DateOnly), err)
	}
	funds, err := store.New(sqlDB).ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = %d funds, %v; want exactly one", len(funds), err)
	}
	return seeded{db: sqlDB, fundID: funds[0].ID, uploads: uploads, now: now, l: ledger.New(sqlDB, ledger.WithClock(func() time.Time { return now }))}
}

func (s seeded) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s = %v", query, err)
	}
	return n
}

func (s seeded) text(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s = %v", query, err)
	}
	return v.String
}

func (s seeded) id(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&id); err != nil {
		t.Fatalf("%s %v = %v", query, args, err)
	}
	return id
}

// periodAt is the "YYYY-MM" of month offset from now's month in the fund's zone,
// worked out here by its own arithmetic and not by the seeder's calendar.
func periodAt(t *testing.T, now time.Time, offset int) string {
	t.Helper()
	local := now.In(wib(t))
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, offset, 0).Format("2006-01")
}

// ADR-045 decision 5: "The fixture is tested on a dozen fixed todays". Each
// must replay with no ledger refusal, end on a matching cek kas, and give a grid
// exactly twelve months wide. The instants are given in WIB and the first one is
// still the 31st in UTC, so a seeder reading the server's zone would date its
// whole story a day early.
func TestFixtureOnADozenFixedTodays(t *testing.T) {
	t.Parallel()
	loc := wib(t)
	todays := []struct {
		name string
		now  time.Time
	}{
		{"1 January, just after midnight in Jakarta", time.Date(2026, 1, 1, 0, 30, 0, 0, loc)},
		{"1 January of a leap year", time.Date(2028, 1, 1, 9, 0, 0, 0, loc)},
		{"2 January", time.Date(2027, 1, 2, 9, 0, 0, 0, loc)},
		{"28 February, last day of an ordinary one", time.Date(2027, 2, 28, 23, 0, 0, 0, loc)},
		{"29 February of a leap year", time.Date(2028, 2, 29, 23, 59, 0, 0, loc)},
		{"1 March after a leap February", time.Date(2028, 3, 1, 0, 0, 0, 0, loc)},
		{"1st of an ordinary month", time.Date(2026, 10, 1, 8, 0, 0, 0, loc)},
		{"mid-month", time.Date(2026, 10, 15, 14, 0, 0, 0, loc)},
		{"last day of a 30-day month", time.Date(2027, 4, 30, 18, 0, 0, 0, loc)},
		{"last day of a 31-day month", time.Date(2027, 7, 31, 18, 0, 0, 0, loc)},
		{"31 December", time.Date(2026, 12, 31, 12, 0, 0, 0, loc)},
		{"the 26th, the day this slice was written", time.Date(2026, 10, 26, 10, 0, 0, 0, loc)},
		{"the 3rd, before most of the story's days", time.Date(2027, 5, 3, 10, 0, 0, 0, loc)},
	}
	if len(todays) < 12 {
		t.Fatalf("%d todays, want a dozen at least", len(todays))
	}
	for _, tc := range todays {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := seedAt(t, tc.now)
			ctx := context.Background()
			today := tc.now.In(loc).Format(time.DateOnly)

			// The latest cek kas matches, to the rupiah, on every location, and was
			// taken at the boot itself.
			latest, err := store.New(s.db).LatestReconciliation(ctx, s.fundID)
			if err != nil {
				t.Fatalf("LatestReconciliation() = %v", err)
			}
			if latest.PerformedAt != tc.now.Unix() {
				t.Errorf("the latest cek kas was taken at %d, want the boot's now %d", latest.PerformedAt, tc.now.Unix())
			}
			detail, err := s.l.GetReconciliationDetail(ctx, s.fundID, latest.ID)
			if err != nil {
				t.Fatalf("GetReconciliationDetail() = %v", err)
			}
			if len(detail.Lines) != 2 {
				t.Fatalf("the latest cek kas has %d lines, want one per location", len(detail.Lines))
			}
			for _, line := range detail.Lines {
				if line.Resolution != string(ledger.ResolutionMatched) || line.DifferenceAmount != 0 || line.RemainderAmount != 0 || line.RecordedAmount != line.ActualAmount {
					t.Errorf("latest cek kas line = %+v, want matched with no difference", line)
				}
			}

			// The grid is exactly twelve months wide and ends on this month, however
			// the history is clamped.
			grid, err := s.l.DuesGrid(ctx, s.fundID, "")
			if err != nil {
				t.Fatalf("DuesGrid() = %v", err)
			}
			if len(grid.Periods) != 12 || grid.Periods[0] != periodAt(t, tc.now, -11) || grid.Periods[11] != periodAt(t, tc.now, 0) {
				t.Errorf("grid periods = %v, want the twelve months from %s to %s", grid.Periods, periodAt(t, tc.now, -11), periodAt(t, tc.now, 0))
			}
			if len(grid.Rows) < 10 {
				t.Errorf("grid has %d rows, want the fund's members", len(grid.Rows))
			}

			// History runs from the first of M-13 to today, and nothing is dated or
			// stamped after the boot.
			if got, want := s.text(t, `SELECT MIN(occurred_on) FROM "transaction"`), periodAt(t, tc.now, -13)+"-01"; got != want {
				t.Errorf("earliest entry = %s, want %s", got, want)
			}
			if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE occurred_on > ?`, today); n != 0 {
				t.Errorf("%d entries are dated after today (%s), want none", n, today)
			}
			if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE created_at > ?`, tc.now.Unix()); n != 0 {
				t.Errorf("%d entries are stamped after the boot, want none", n)
			}
			if n := s.count(t, `SELECT COUNT(*) FROM reconciliation WHERE performed_at > ?`, tc.now.Unix()); n != 0 {
				t.Errorf("%d cek kas are stamped after the boot, want none", n)
			}
			if n := s.count(t, `SELECT COUNT(*) FROM reconciliation`); n != 14 {
				t.Errorf("%d cek kas, want 14 (the setup one and one a month after)", n)
			}
			if n := s.count(t, `SELECT COUNT(*) FROM receipt`); n != 3 {
				t.Errorf("%d receipts, want 3", n)
			}
		})
	}
}

// The fixture covers every case ADR-045 names, so each screen has its example.
// One fixed today; the cases do not depend on it.
func TestFixtureCoversEveryCase(t *testing.T) {
	t.Parallel()
	s := seedAt(t, fixedNow())
	ctx := context.Background()
	now := s.now
	q := store.New(s.db)

	memberID := func(name string) int64 {
		return s.id(t, `SELECT id FROM member WHERE name = ?`, name)
	}
	envelope := func(occasion string) int64 {
		return s.id(t, `SELECT purpose_id FROM incidental WHERE occasion = ?`, occasion)
	}

	t.Run("roughly 350 entries, about 25 a month", func(t *testing.T) {
		total := s.count(t, `SELECT COUNT(*) FROM "transaction"`)
		t.Logf("%d entries", total)
		if total < 280 || total > 420 {
			t.Errorf("%d entries, want roughly 350", total)
		}
		perMonth := map[string]int{}
		rows, err := s.db.QueryContext(ctx, `SELECT substr(occurred_on, 1, 7), COUNT(*) FROM "transaction" GROUP BY 1`)
		if err != nil {
			t.Fatalf("counting per month: %v", err)
		}
		for rows.Next() {
			var month string
			var n int
			if err := rows.Scan(&month, &n); err != nil {
				t.Fatalf("scanning: %v", err)
			}
			perMonth[month] = n
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("closing: %v", err)
		}
		months := len(perMonth)
		t.Logf("entries per month: %v", perMonth)
		for month, n := range perMonth {
			if n < 15 {
				t.Errorf("%s has %d entries, want a month that reads like a real one", month, n)
			}
		}
		if months != 14 {
			t.Errorf("entries span %d months, want 14 (M-13 to M0)", months)
		}
	})

	t.Run("opening balances on cash and bank", func(t *testing.T) {
		if got := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE kind = 'opening'`); got != 2 {
			t.Errorf("%d opening balances, want one on cash and one on bank", got)
		}
		if got := s.text(t, `SELECT occurred_on FROM "transaction" WHERE kind = 'opening' LIMIT 1`); got != periodAt(t, now, -13)+"-01" {
			t.Errorf("opening balance dated %s, want the first day of M-13", got)
		}
	})

	t.Run("the three envelope participation states and an anonymous contribution", func(t *testing.T) {
		purposeID := envelope("Kelahiran anak Mbak Sari")
		part, err := s.l.GetIncidentalParticipation(ctx, s.fundID, purposeID)
		if err != nil {
			t.Fatalf("GetIncidentalParticipation() = %v", err)
		}
		states := map[ledger.ParticipationState]int{}
		for _, e := range part.Expected {
			states[e.State]++
		}
		for _, want := range []ledger.ParticipationState{ledger.ParticipationSudah, ledger.ParticipationBelum, ledger.ParticipationKurang} {
			if states[want] == 0 {
				t.Errorf("no member in state %q on the open envelope; states = %v", want, states)
			}
		}
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE purpose_id = ? AND direction = 'in' AND member_id IS NULL`, purposeID); n == 0 {
			t.Error("no anonymous contribution to the open envelope")
		}
		if closed := s.text(t, `SELECT closed_on FROM incidental WHERE purpose_id = ?`, purposeID); closed != "" {
			t.Errorf("the baby envelope is closed (%s), want it open", closed)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM incidental_recipient WHERE purpose_id = ?`, purposeID); n != 1 {
			t.Errorf("%d recipients, want Mbak Sari alone", n)
		}
		if minimum := s.count(t, `SELECT minimum_per_member FROM incidental WHERE purpose_id = ?`, purposeID); minimum <= 0 {
			t.Error("the open envelope has no minimum per member")
		}
	})

	t.Run("a farewell envelope closed and rolled into Kas Utama", func(t *testing.T) {
		purposeID := envelope("Perpisahan Bu Erna")
		if closed := s.text(t, `SELECT closed_on FROM incidental WHERE purpose_id = ?`, purposeID); closed == "" {
			t.Error("the farewell envelope is still open")
		}
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" x JOIN transfer t ON t.id = x.transfer_id WHERE t.reason = 'roll' AND x.purpose_id = ?`, purposeID); n != 1 {
			t.Errorf("%d roll legs on the envelope, want its leftover leaving once", n)
		}
		if bal, err := s.l.PurposeBalance(ctx, s.fundID, purposeID); err != nil || bal != 0 {
			t.Errorf("closed envelope balance = %d, %v; want 0 after the roll", bal, err)
		}
		recipient := s.id(t, `SELECT member_id FROM incidental_recipient WHERE purpose_id = ?`, purposeID)
		if recipient != memberID("Bu Erna") {
			t.Errorf("recipient = %d, want Bu Erna", recipient)
		}
	})

	t.Run("an envelope reopened for late contributions", func(t *testing.T) {
		purposeID := envelope("Jenguk Pak Arga")
		if closed := s.text(t, `SELECT closed_on FROM incidental WHERE purpose_id = ?`, purposeID); closed != "" {
			t.Errorf("closed on %s, want it reopened", closed)
		}
		rolledOn := s.text(t, `SELECT MAX(x.occurred_on) FROM "transaction" x JOIN transfer t ON t.id = x.transfer_id WHERE t.reason = 'roll' AND x.purpose_id = ?`, purposeID)
		if rolledOn == "" {
			t.Fatal("the envelope was never rolled, so it was never closed")
		}
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE purpose_id = ? AND kind = 'normal' AND direction = 'in' AND occurred_on > ?`, purposeID, rolledOn); n == 0 {
			t.Error("no contribution came in after the envelope closed")
		}
		if bal, err := s.l.PurposeBalance(ctx, s.fundID, purposeID); err != nil || bal <= 0 {
			t.Errorf("reopened envelope balance = %d, %v; want the late contributions", bal, err)
		}
	})

	t.Run("a pass-through collected and forwarded, never touching what the fund owns", func(t *testing.T) {
		purposeID := s.id(t, `SELECT id FROM purpose WHERE kind = 'pass_through'`)
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE purpose_id = ? AND direction = 'in'`, purposeID); n < 2 {
			t.Errorf("%d collections, want several", n)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE purpose_id = ? AND direction = 'out'`, purposeID); n < 1 {
			t.Error("nothing forwarded")
		}
		if bal, err := s.l.PurposeBalance(ctx, s.fundID, purposeID); err != nil || bal != 0 {
			t.Errorf("pass-through balance = %d, %v; want everything forwarded", bal, err)
		}
	})

	t.Run("reimbursements: one settled with a receipt, one still owed", func(t *testing.T) {
		if n := s.count(t, `SELECT COUNT(*) FROM reimbursement`); n != 2 {
			t.Errorf("%d reimbursements, want two", n)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM "transaction" WHERE kind = 'reimbursement'`); n != 1 {
			t.Errorf("%d settlements, want one", n)
		}
		owed, err := s.l.OwedToMembers(ctx, s.fundID)
		if err != nil || owed != 120_000 {
			t.Errorf("owed to members = %d, %v; want the one unsettled claim, 120000", owed, err)
		}
	})

	t.Run("movements: a deposit, a Pindah pos, a purpose correction, a reversed expense and a reversed dues payment", func(t *testing.T) {
		checks := []struct{ what, query string }{
			{"cash moved between locations", `SELECT COUNT(*) FROM transfer WHERE kind = 'between_accounts'`},
			{"a Pindah pos", `SELECT COUNT(*) FROM transfer WHERE reason = 'allocation'`},
			{"a purpose correction", `SELECT COUNT(*) FROM transfer WHERE corrects_transaction_id IS NOT NULL`},
			{"a reversed ordinary expense", `SELECT COUNT(*) FROM "transaction" r JOIN "transaction" o ON o.id = r.reverses_transaction_id WHERE o.kind = 'normal'`},
			{"a reversed dues payment", `SELECT COUNT(*) FROM "transaction" r JOIN "transaction" o ON o.id = r.reverses_transaction_id WHERE o.kind = 'dues'`},
		}
		for _, c := range checks {
			if s.count(t, c.query) == 0 {
				t.Errorf("no %s", c.what)
			}
		}
	})

	t.Run("cek kas: all cocok, one settled with an adjusted plug", func(t *testing.T) {
		if n := s.count(t, `SELECT COUNT(*) FROM reconciliation_line WHERE resolution = 'adjusted'`); n != 1 {
			t.Errorf("%d adjusted lines, want exactly one", n)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM reconciliation_line WHERE resolution NOT IN ('matched', 'adjusted')`); n != 0 {
			t.Errorf("%d lines left open or entered, want every count cocok", n)
		}
		// The plug is the dues paid before Uruni, out of cash: Pak Arga three
		// months, Mbak Dewi and Bu Erna two each, at the first rates.
		if got, want := s.count(t, `SELECT -difference_amount FROM reconciliation_line WHERE resolution = 'adjusted'`), 3*40_000+2*40_000+2*60_000; got != want {
			t.Errorf("plug = %d, want the prepaid dues %d", got, want)
		}
	})

	t.Run("dues: every status, two members behind, rates that rose, corrections, a gap in tiers", func(t *testing.T) {
		statuses, err := s.l.DuesStatusForPeriod(ctx, s.fundID, periodAt(t, now, 0))
		if err != nil {
			t.Fatalf("DuesStatusForPeriod() = %v", err)
		}
		seen := map[ledger.DuesStatus]bool{}
		for _, st := range statuses {
			seen[st.Status] = true
		}
		for _, want := range []ledger.DuesStatus{ledger.DuesStatusUnpaid, ledger.DuesStatusPartial, ledger.DuesStatusPaid, ledger.DuesStatusPaidInAdvance} {
			if !seen[want] {
				t.Errorf("no member is %q this month; statuses = %v", want, seen)
			}
		}

		behind := 0
		for _, name := range []string{"Mas Rizki", "Mbak Wulan"} {
			months, err := s.l.ArrearsMonthsForMember(ctx, s.fundID, memberID(name), periodAt(t, now, 0))
			if err != nil {
				t.Fatalf("ArrearsMonthsForMember(%s) = %v", name, err)
			}
			if months >= 3 {
				behind++
			}
		}
		if behind != 2 {
			t.Errorf("%d members behind for several months, want two", behind)
		}

		owedIn := func(period, name string) int64 {
			t.Helper()
			rows, err := s.l.DuesStatusForPeriod(ctx, s.fundID, period)
			if err != nil {
				t.Fatalf("DuesStatusForPeriod(%s) = %v", period, err)
			}
			for _, r := range rows {
				if r.Member.Name == name {
					return r.OwedAmount.Int64()
				}
			}
			return -1
		}
		// Every rate rises in M-6; the Team Leader one was typed wrongly first.
		for _, c := range []struct {
			name       string
			offset     int
			want       int64
			whatItWas  string
			whatItIsNo string
		}{
			{"Mas Pandu", -7, 25_000, "Agen", "before"},
			{"Mas Pandu", -6, 30_000, "Agen", "from M-6"},
			{"Mbak Dewi", -7, 40_000, "Team Leader", "before"},
			{"Mbak Dewi", -6, 50_000, "Team Leader", "from M-6, corrected from 55000"},
			{"Bu Erna", -9, 60_000, "Supervisor", "before"},
		} {
			if got := owedIn(periodAt(t, now, c.offset), c.name); got != c.want {
				t.Errorf("%s owes %d in M%d (%s, %s), want %d", c.name, got, c.offset, c.whatItWas, c.whatItIsNo, c.want)
			}
		}
		if n := s.count(t, `SELECT COUNT(*) FROM dues_rate WHERE correction > 0`); n != 1 {
			t.Errorf("%d corrected rates, want one", n)
		}

		// A tier corrected (Mas Fajar), a member Tanpa golongan for a stretch (Pak
		// Lingga), a promotion (Mbak Indri).
		if n := s.count(t, `SELECT COUNT(*) FROM member_tier WHERE correction > 0 AND member_id = ?`, memberID("Mas Fajar")); n != 1 {
			t.Errorf("%d tier corrections for Mas Fajar, want one", n)
		}
		if got := owedIn(periodAt(t, now, -3), "Mas Fajar"); got != 30_000 {
			t.Errorf("Mas Fajar owes %d in M-3, want the Agen rate 30000 after the correction", got)
		}
		for _, offset := range []int{-9, -8} {
			if got := owedIn(periodAt(t, now, offset), "Pak Lingga"); got != -1 {
				t.Errorf("Pak Lingga owes %d in M%d, want no obligation (Tanpa golongan)", got, offset)
			}
		}
		if got := owedIn(periodAt(t, now, -7), "Pak Lingga"); got != 25_000 {
			t.Errorf("Pak Lingga owes %d in M-7, want the Agen rate again", got)
		}
		if got := owedIn(periodAt(t, now, -6), "Mbak Indri"); got != 30_000 {
			t.Errorf("Mbak Indri owes %d in M-6, want Agen", got)
		}
		if got := owedIn(periodAt(t, now, -5), "Mbak Indri"); got != 50_000 {
			t.Errorf("Mbak Indri owes %d in M-5, want Team Leader after her promotion", got)
		}
	})

	t.Run("the roster changes: a departure, a resignation, two arrivals, and an empty Supervisor tier", func(t *testing.T) {
		inactive := func(name string) string {
			return s.text(t, `SELECT inactive_on FROM member WHERE name = ?`, name)
		}
		if got := inactive("Bu Erna")[:7]; got != periodAt(t, now, -8) {
			t.Errorf("Bu Erna left in %s, want M-8", got)
		}
		if got := inactive("Mas Yoga")[:7]; got != periodAt(t, now, -4) {
			t.Errorf("Mas Yoga left in %s, want M-4", got)
		}
		joined := func(name string) string {
			return s.text(t, `SELECT joined_on FROM member WHERE name = ?`, name)[:7]
		}
		if got := joined("Mbak Anisa"); got != periodAt(t, now, -10) {
			t.Errorf("Mbak Anisa joined in %s, want M-10", got)
		}
		if got := joined("Mas Fajar"); got != periodAt(t, now, -3) {
			t.Errorf("Mas Fajar joined in %s, want M-3", got)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM member`); n != 13 {
			t.Errorf("%d members, want 13 (eleven at the start and two who joined)", n)
		}
		if n := s.count(t, `SELECT COUNT(*) FROM member WHERE joined_on < ?`, periodAt(t, now, -10)+"-01"); n != 11 {
			t.Errorf("%d members from the first month, want 11", n)
		}

		supervisor := s.id(t, `SELECT id FROM dues_tier WHERE name = 'Supervisor'`)
		for _, c := range []struct {
			offset int
			want   int
		}{{-9, 1}, {-8, 1}, {-7, 0}, {-3, 0}, {0, 0}} {
			rows, err := s.l.DuesStatusForPeriod(ctx, s.fundID, periodAt(t, now, c.offset))
			if err != nil {
				t.Fatalf("DuesStatusForPeriod() = %v", err)
			}
			got := 0
			for _, r := range rows {
				if r.Member.TierID != nil && *r.Member.TierID == supervisor {
					got++
				}
			}
			if got != c.want {
				t.Errorf("%d members owe at the Supervisor tier in M%d, want %d", got, c.offset, c.want)
			}
		}
		if n := s.count(t, `SELECT COUNT(*) FROM dues_tier`); n != 3 {
			t.Errorf("%d tiers, want Agen, Team Leader and Supervisor", n)
		}
	})

	t.Run("receipts on the settled reimbursement and on an expense, drawn and processed", func(t *testing.T) {
		type receiptRow struct {
			path      string
			tx, claim *int64
		}
		var receipts []receiptRow
		rows, err := s.db.QueryContext(ctx, `SELECT path, transaction_id, reimbursement_id FROM receipt`)
		if err != nil {
			t.Fatalf("reading the receipts: %v", err)
		}
		for rows.Next() {
			var r receiptRow
			if err := rows.Scan(&r.path, &r.tx, &r.claim); err != nil {
				t.Fatalf("scanning: %v", err)
			}
			receipts = append(receipts, r)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("closing: %v", err)
		}
		onClaim, onExpense := 0, 0
		for _, r := range receipts {
			f, err := os.Open(filepath.Join(s.uploads, r.path))
			if err != nil {
				t.Errorf("receipt file %s: %v", r.path, err)
				continue
			}
			cfg, format, err := image.DecodeConfig(f)
			_ = f.Close()
			if err != nil || format != "jpeg" || cfg.Width < 200 || cfg.Height < 100 {
				t.Errorf("receipt %s = %dx%d %s, %v; want a stored JPEG", r.path, cfg.Width, cfg.Height, format, err)
			}
			switch {
			case r.claim != nil:
				onClaim++
			case r.tx != nil:
				if kind := s.text(t, `SELECT kind FROM "transaction" WHERE id = ?`, *r.tx); kind != "normal" {
					t.Errorf("receipt on a %s row, want an ordinary expense", kind)
				}
				onExpense++
			}
		}
		if onClaim != 2 || onExpense != 1 {
			t.Errorf("receipts on %d claims and %d expenses, want 2 and 1 (the settled claim, the owed one, one expense)", onClaim, onExpense)
		}
		if settled := s.count(t, `SELECT COUNT(*) FROM receipt r JOIN "transaction" x ON x.reimbursement_id = r.reimbursement_id WHERE x.kind = 'reimbursement'`); settled != 1 {
			t.Errorf("%d receipts on the settled reimbursement, want one", settled)
		}
	})

	t.Run("the books agree with themselves", func(t *testing.T) {
		balances, err := s.l.Balances(ctx, s.fundID)
		if err != nil {
			t.Fatalf("Balances() = %v", err)
		}
		var accounts int64
		for _, a := range balances.Accounts {
			if a.Balance < 0 {
				t.Errorf("account %q is negative: %d", a.Account.Name, a.Balance)
			}
			accounts += a.Balance.Int64()
		}
		if accounts != balances.FundTotal.Int64() {
			t.Errorf("the accounts add up to %d, the fund total is %d", accounts, balances.FundTotal.Int64())
		}
		for _, p := range balances.Purposes {
			if p.Balance < 0 {
				t.Errorf("purpose %q is negative: %d", p.Purpose.Name, p.Balance)
			}
		}
		latest, err := q.LatestReconciliation(ctx, s.fundID)
		if err != nil {
			t.Fatalf("LatestReconciliation() = %v", err)
		}
		detail, err := s.l.GetReconciliationDetail(ctx, s.fundID, latest.ID)
		if err != nil {
			t.Fatalf("GetReconciliationDetail() = %v", err)
		}
		var counted int64
		for _, line := range detail.Lines {
			counted += line.ActualAmount
		}
		if counted != balances.FundTotal.Int64() {
			t.Errorf("the latest count found %d, the ledger records %d", counted, balances.FundTotal.Int64())
		}
	})
}

// Seeding is well under a second at boot (ADR-045). The test runs against a
// file database, as production does, and holds it to a loose bound so a slow
// shared runner (or -race) does not fail it; BenchmarkSeed is the figure to read.
// TestSeedStaysQuick holds the seed to ADR-045's "well under a second" at boot.
// Speed is a property of the release build, so it is skipped under -race,
// which instruments the pure-Go SQLite driver and runs the seed some 16x
// slower; BenchmarkSeed is the number to read.
func TestSeedStaysQuick(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is meaningless under -race")
	}
	sqlDB := newFileDB(t)
	uploads := t.TempDir()
	start := time.Now()
	if err := Ensure(context.Background(), sqlDB, uploads, fixedNow); err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("seeded on a file database in %v", elapsed)
	if elapsed > time.Second {
		t.Errorf("seeding took %v, want well under a second", elapsed)
	}
}

func BenchmarkSeed(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		dir := b.TempDir()
		sqlDB, err := db.Open(context.Background(), filepath.Join(dir, "bench.db"))
		if err != nil {
			b.Fatalf("db.Open() = %v", err)
		}
		if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
			b.Fatalf("db.Up() = %v", err)
		}
		uploads := filepath.Join(dir, "uploads")
		if err := os.Mkdir(uploads, 0o750); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		b.StartTimer() // running again before the next b.Loop, which refuses a stopped timer
		if err := Ensure(context.Background(), sqlDB, uploads, fixedNow); err != nil {
			b.Fatalf("Ensure() = %v", err)
		}
		_ = sqlDB.Close()
	}
}

func newFileDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("db.Open() = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	return sqlDB
}
