package db

import (
	"context"
	"regexp"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// ledger_entry (#627, #628) defines three derived facts once: the signed amount,
// the reversed flag and the effective pos. These tests hold the view
// itself, and then hold every query to reading them from it rather than writing
// them again.

// TestLedgerEntryDefinesTheSignAndTheReversal pins the view's columns against a
// ledger with an inflow, an outflow, and a dues payment that a later reversal
// undoes.
func TestLedgerEntryDefinesTheSignAndTheReversal(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	period := "2026-08"

	inID := f.post(t, sqlDB, "in", 500000)
	outID := f.post(t, sqlDB, "out", 120000)
	payment, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-12", Kind: "dues",
		MemberID: &f.memberID, DuesPeriod: &period, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the dues payment = %v, want no error", err)
	}
	reversal, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: f.purposeID,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-20", Kind: "adjustment",
		MemberID: &f.memberID, DuesPeriod: &period, ReversesTransactionID: &payment.ID,
		CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the reversal = %v, want no error", err)
	}

	type entry struct {
		signed     int64
		reversed   int64
		reversedBy *int64
		reversedOn *string
	}
	read := func(id int64) entry {
		t.Helper()
		var e entry
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT signed_amount, reversed, reversed_by_transaction_id, reversed_on FROM ledger_entry WHERE id = ?`, id,
		).Scan(&e.signed, &e.reversed, &e.reversedBy, &e.reversedOn); err != nil {
			t.Fatalf("reading ledger_entry %d = %v, want no error", id, err)
		}
		return e
	}

	day := "2026-08-20"
	for _, tc := range []struct {
		name string
		id   int64
		want entry
	}{
		{"an inflow is positive and stands", inID, entry{signed: 500000}},
		{"an outflow is negative and stands", outID, entry{signed: -120000}},
		{"a reversed payment is flagged, by the reversal, on its date", payment.ID, entry{signed: 25000, reversed: 1, reversedBy: &reversal.ID, reversedOn: &day}},
		{"the reversal itself is negative and is never reversed", reversal.ID, entry{signed: -25000}},
	} {
		got := read(tc.id)
		if got.signed != tc.want.signed || got.reversed != tc.want.reversed ||
			!equalPtr(got.reversedBy, tc.want.reversedBy) || !equalPtr(got.reversedOn, tc.want.reversedOn) {
			t.Errorf("%s: ledger_entry = %+v, want %+v", tc.name, got, tc.want)
		}
	}

	// The view is a window, not a copy: one row per transaction, none added or
	// dropped, so a sum over it is a sum over the ledger.
	var rows, txs int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM ledger_entry`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM "transaction"`).Scan(&txs); err != nil {
		t.Fatal(err)
	}
	if rows != txs {
		t.Errorf("ledger_entry has %d rows, \"transaction\" has %d, want them equal", rows, txs)
	}
}

// TestLedgerEntryDefinesTheEffectivePurpose pins effective_purpose_id (ADR-033,
// #628) against corrections written the way PostPurposeCorrection writes them:
// a pair whose leg moving the row's way carries the target. A row nothing
// corrects, and every transfer leg, answer their own stored tag; a corrected
// row answers where its corrections end; and the answer does not depend on the
// order the corrections' rows were written in.
func TestLedgerEntryDefinesTheEffectivePurpose(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	pass := createPurpose(t, sqlDB, f.fundID, "pass_through", "Pass-through")
	other := createPurpose(t, sqlDB, f.fundID, "pass_through", "Other pass-through")
	main := f.purposeID

	post := func(direction string, purposeID int64, transferID *int64) int64 {
		t.Helper()
		kind := "normal"
		if transferID != nil {
			kind = "transfer"
		}
		row, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: f.fundID, AccountID: f.accountID, PurposeID: purposeID,
			Direction: direction, Amount: 40000, OccurredOn: "2026-08-12", Kind: kind,
			TransferID: transferID, CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("CreateTransaction(%s, purpose %d) = %v, want no error", direction, purposeID, err)
		}
		return row.ID
	}
	// correct writes one correction of the row the way PostPurposeCorrection
	// does: from is the effective tag the pair takes the money out of, to the
	// target it moves it into, and an 'out' row swaps which leg is which.
	correct := func(rowID int64, rowDirection string, from, to int64) {
		t.Helper()
		tr, err := q.CreateTransfer(ctx, store.CreateTransferParams{
			FundID: f.fundID, Kind: "reclass_purpose", CorrectsTransactionID: &rowID, CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("CreateTransfer(corrects %d) = %v, want no error", rowID, err)
		}
		out, in := from, to
		if rowDirection == "out" {
			out, in = to, from
		}
		post("out", out, &tr.ID)
		post("in", in, &tr.ID)
	}
	effective := func(id int64) int64 {
		t.Helper()
		var got int64
		if err := sqlDB.QueryRowContext(ctx, `SELECT effective_purpose_id FROM ledger_entry WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("reading ledger_entry %d = %v, want no error", id, err)
		}
		return got
	}

	untouched := post("in", main, nil)
	for _, direction := range []string{"in", "out"} {
		once := post(direction, main, nil)
		correct(once, direction, main, pass)

		// main -> pass -> other -> pass, written as pass -> other, other -> pass,
		// then main -> pass: the first correction has the highest id, so the
		// answer cannot come from id order.
		thrice := post(direction, main, nil)
		correct(thrice, direction, pass, other)
		correct(thrice, direction, other, pass)
		correct(thrice, direction, main, pass)

		back := post(direction, main, nil)
		correct(back, direction, main, pass)
		correct(back, direction, pass, main)

		for _, tc := range []struct {
			name string
			id   int64
			want int64
		}{
			{direction + " row corrected once", once, pass},
			{direction + " row corrected three times, written out of order", thrice, pass},
			{direction + " row corrected and corrected back", back, main},
		} {
			if got := effective(tc.id); got != tc.want {
				t.Errorf("%s: effective_purpose_id = %d, want %d", tc.name, got, tc.want)
			}
		}
	}
	if got := effective(untouched); got != main {
		t.Errorf("an uncorrected row: effective_purpose_id = %d, want its stored tag %d", got, main)
	}

	// Every row that is not corrected - legs included - answers its own tag, and
	// the stored tag is still what the table holds: the view adds, it never edits.
	var disagree int64
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT count(*) FROM ledger_entry e
		WHERE e.kind = 'transfer' AND e.effective_purpose_id <> e.purpose_id`).Scan(&disagree); err != nil {
		t.Fatal(err)
	}
	if disagree != 0 {
		t.Errorf("%d transfer legs have an effective purpose other than their stored one, want 0", disagree)
	}
	var rows, txs int64
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM ledger_entry`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM "transaction"`).Scan(&txs); err != nil {
		t.Fatal(err)
	}
	if rows != txs {
		t.Errorf("ledger_entry has %d rows, \"transaction\" has %d, want them equal", rows, txs)
	}
}

// TestAReversalIsCountedFromItsOwnDate pins the as-of boundary the public report
// relies on (#408) through ledger_entry.reversed_on: a named contribution is
// dropped, with its reversal, only once the reversal's date is on or before
// through. NULL through is "now", where any reversal counts.
func TestAReversalIsCountedFromItsOwnDate(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newLedgerFixture(t, sqlDB, "Test Fund", validSlug)
	envelope := createPurpose(t, sqlDB, f.fundID, "incidental", "Sunatan")

	gift, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelope,
		Direction: "in", Amount: 25000, OccurredOn: "2026-08-10", Kind: "normal",
		MemberID: &f.memberID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("posting the contribution = %v, want no error", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.accountID, PurposeID: envelope,
		Direction: "out", Amount: 25000, OccurredOn: "2026-08-20", Kind: "adjustment",
		MemberID: &f.memberID, ReversesTransactionID: &gift.ID, CreatedAt: 2,
	}); err != nil {
		t.Fatalf("posting the reversal = %v, want no error", err)
	}

	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name      string
		through   *string
		wantGiven int64 // what ContributedByIncidentalMember and IncidentalActivityTotals still count
	}{
		{"a reversal dated exactly on through is already in force", str("2026-08-20"), 0},
		{"a reversal dated the day after through has not happened yet", str("2026-08-19"), 25000},
		{"with no through, a reversal is in force", nil, 0},
	} {
		contributed, err := q.ContributedByIncidentalMember(ctx, store.ContributedByIncidentalMemberParams{
			FundID: f.fundID, PurposeID: envelope, Through: tc.through,
		})
		if err != nil {
			t.Fatalf("%s: ContributedByIncidentalMember = %v, want no error", tc.name, err)
		}
		var gave int64
		for _, r := range contributed {
			gave += r.ContributedAmount
		}
		if gave != tc.wantGiven {
			t.Errorf("%s: ContributedByIncidentalMember = %d, want %d", tc.name, gave, tc.wantGiven)
		}

		totals, err := q.IncidentalActivityTotals(ctx, store.IncidentalActivityTotalsParams{
			FundID: f.fundID, PurposeID: envelope, Through: tc.through,
		})
		if err != nil {
			t.Fatalf("%s: IncidentalActivityTotals = %v, want no error", tc.name, err)
		}
		if totals.CollectedAmount != tc.wantGiven || totals.DisbursedAmount != 0 {
			t.Errorf("%s: IncidentalActivityTotals = %+v, want collected %d and disbursed 0", tc.name, totals, tc.wantGiven)
		}
	}
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// A hand-written sign takes one of three shapes, each testing direction against
// a literal and then mentioning the unsigned amount before the construct ends:
//
//	CASE WHEN direction = 'in' THEN amount ELSE -amount END   (searched CASE)
//	CASE direction WHEN 'in' THEN amount ELSE -amount END     (simple CASE)
//	IIF(direction = 'in', amount, -amount)
//
// "CASE rl.resolution WHEN 'left_open' ..." is none of them: the operand must be
// a direction column.
var (
	searchedSign = regexp.MustCompile(`(?is)\bWHEN\s+[\w.]*\bdirection\s*=\s*'(?:in|out)'\s+THEN\b(.*?)\bEND\b`)
	simpleSign   = regexp.MustCompile(`(?is)\bCASE\s+[\w.]*\bdirection\s+WHEN\s+'(?:in|out)'\s+THEN\b(.*?)\bEND\b`)
	iifSign      = regexp.MustCompile(`(?is)\bIIF\s*\(\s*[\w.]*\bdirection\s*=\s*'(?:in|out)'\s*,`)
)

// bareAmount is the unsigned column, as "amount" or "alias.amount": not
// signed_amount, collected_amount and the like, whose "amount" follows a word
// character.
var bareAmount = regexp.MustCompile(`(?i)(?:^|[^\w])amount\b`)

// iifArguments returns the text of an IIF call's arguments after the match of
// iifSign, up to the parenthesis that closes the call (a regexp cannot balance
// parentheses).
func iifArguments(body string, from int) string {
	depth := 1
	for i := from; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return body[from:i]
			}
		}
	}
	return body[from:]
}

// derivesTheSign reports whether a query writes the signed amount itself.
func derivesTheSign(body string) bool {
	for _, re := range []*regexp.Regexp{searchedSign, simpleSign} {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			if bareAmount.MatchString(m[1]) {
				return true
			}
		}
	}
	for _, loc := range iifSign.FindAllStringIndex(body, -1) {
		if bareAmount.MatchString(iifArguments(body, loc[1])) {
			return true
		}
	}
	return false
}

// derivesTheReversal matches a query asking, itself, whether a row has been
// reversed: a reference to the rows that name it as reverses_transaction_id
// (r.reverses_transaction_id = t.id). Looking a reversal up by a parameter
// (reverses_transaction_id = ?) and asking what a reversal reverses
// (orig.id = t.reverses_transaction_id) are not that, so those pass.
//
// The one shape this misses is the same test with its operands swapped
// (t.id = r.reverses_transaction_id). It cannot be closed: that spelling is
// textually the same as orig.id = t.reverses_transaction_id, which is a
// legitimate read and must not be flagged. A person reviewing a new query has
// to catch the swapped spelling.
var derivesTheReversal = regexp.MustCompile(`(?i)\breverses_transaction_id\s*=\s*\w+\.id\b`)

// TestTheGuardsSeeEveryShapeOfADerivation pins the guard's own patterns: one
// that missed a shape would let a query re-derive the sign or the reversed flag
// without the guard saying so.
func TestTheGuardsSeeEveryShapeOfADerivation(t *testing.T) {
	t.Parallel()
	for _, sign := range []string{
		`SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END)`,
		`SUM(CASE WHEN t.direction = 'out' THEN -t.amount ELSE t.amount END)`,
		`SUM(CASE WHEN direction = 'in' THEN amount ELSE 0 END)`,
		"CASE\n WHEN fix.direction = 'in' THEN rl.difference_amount - fix.amount\n ELSE rl.difference_amount + fix.amount\n END",
		`CASE WHEN tr.reason = 'allocation' THEN CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END ELSE 0 END`,
		`select sum(case when direction='in' then amount else -amount end)`,
		`SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END)`,
		`SUM(CASE t.direction WHEN 'out' THEN -t.amount WHEN 'in' THEN t.amount END)`,
		"CASE fix.direction\n WHEN 'in' THEN rl.difference_amount - fix.amount\n ELSE rl.difference_amount + fix.amount END",
		`SUM(IIF(direction = 'in', amount, -amount))`,
		`SUM(IIF(t.direction='out', -t.amount, t.amount))`,
		`SUM(IIF(t.direction = 'in', COALESCE(t.amount, 0), -(t.amount)))`,
	} {
		if !derivesTheSign(sign) {
			t.Errorf("the sign guard misses: %q", sign)
		}
	}
	for _, notSign := range []string{
		`SUM(signed_amount)`,
		`SUM(CASE WHEN signed_amount > 0 THEN signed_amount ELSE 0 END)`,
		`CASE WHEN tr.reason = 'allocation' THEN t.signed_amount WHEN COALESCE(o.direction, t.direction) = 'in' THEN t.signed_amount ELSE 0 END`,
		`ELSE rl.difference_amount - fix.signed_amount`,
		`SUM(CASE WHEN leg.direction = t.direction THEN 1 ELSE -1 END)`,
		`WHERE t.kind = 'normal' AND t.direction = 'in' GROUP BY member_id`,
		`SELECT t.direction, t.amount FROM ledger_entry t WHERE t.direction = 'in'`,
		`CAST(CASE rl.resolution WHEN 'left_open' THEN rl.difference_amount WHEN 'matched' THEN 0 ELSE 1 END AS INTEGER)`,
		`CASE rl.resolution WHEN 'left_open' THEN rl.difference_amount ELSE 0 END, t.amount`,
		`SUM(CASE direction WHEN 'in' THEN signed_amount ELSE 0 END)`,
		`SUM(IIF(signed_amount > 0, signed_amount, 0))`,
		`SUM(IIF(direction = 'in', 1, 0)) + SUM(amount)`,
		`SUM(IIF(t.kind = 'dues', t.amount, 0))`,
	} {
		if derivesTheSign(notSign) {
			t.Errorf("the sign guard flags something that is not a derivation: %q", notSign)
		}
	}

	for _, reversal := range []string{
		`AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)`,
		`LEFT JOIN "transaction" rv ON rv.reverses_transaction_id = t.id`,
		`WHERE reverses_transaction_id=x.id`,
		"WHERE r.reverses_transaction_id\n = t.id",
		`where R.REVERSES_TRANSACTION_ID = T.ID`,
	} {
		if !derivesTheReversal.MatchString(reversal) {
			t.Errorf("the reversal guard misses: %q", reversal)
		}
	}
	for _, notReversal := range []string{
		`WHERE fund_id = ? AND reverses_transaction_id = ?`,
		`LEFT JOIN "transaction" orig ON orig.id = t.reverses_transaction_id`,
		`AND t.reverses_transaction_id IS NULL`,
		`AND t.reversed = 0`,
		`t.reversed_by_transaction_id`,
	} {
		if derivesTheReversal.MatchString(notReversal) {
			t.Errorf("the reversal guard flags something that is not a derivation: %q", notReversal)
		}
	}
}

// TestNoQueryDerivesTheSignOrTheReversalOutsideTheView is #627's guard: the
// signed amount and the reversed flag are defined in 00001_schema.sql's
// ledger_entry and nowhere else, so a new query that wants either selects it
// from the view. A query that wrote its own sign could disagree with every
// balance about which way a row moves; one that wrote its own reversal test
// could disagree about which rows still count. Reversal (ADR-042) changed the
// second in exactly one place, the view.
func TestNoQueryDerivesTheSignOrTheReversalOutsideTheView(t *testing.T) {
	t.Parallel()
	for name, body := range queryStatements(t) {
		if derivesTheSign(body) {
			t.Errorf("query %s writes the signed amount itself; select ledger_entry.signed_amount", name)
		}
		if derivesTheReversal.MatchString(body) {
			t.Errorf("query %s asks itself whether a row is reversed; select ledger_entry.reversed, reversed_on or reversed_by_transaction_id", name)
		}
	}
}

// TestTheLedgerReadsSelectFromTheView holds the other half: the queries that
// sum the ledger or ask about reversals must read ledger_entry, so the guard
// above cannot be satisfied by a query that quietly dropped the sign (or the
// reversal exclusion) instead of moving it to the view.
func TestTheLedgerReadsSelectFromTheView(t *testing.T) {
	t.Parallel()
	queries := queryStatements(t)
	for _, name := range []string{
		// balances
		"FundBalance", "AccountBalance", "PurposeBalance", "AccountBalanceThrough",
		"ReportFundBalance", "ReportPurposeBalance",
		// the envelope
		"IncidentalTotals", "IncidentalActivityTotals", "ContributedByIncidentalMember",
		// dues
		"DuesPaidByPeriod", "DuesPaidByMemberGroupedByPeriod", "LatestDuesPeriodPaidByMember", "ListDuesPaymentsPage",
		// reversal (ADR-042): the target's facts and the reverse-then-correct refusal
		"GetReversalTarget", "ReversedFlagForTransaction", "ListTransactionsPage", "ListReportTransactions",
		// a reconciliation line's remainder reads its fix's signed amount
		"ListReconciliationLines", "ListOpenReconciliationLinesByFund", "ListReconciliationsPage", "ReportOpenReconciliationLines",
	} {
		body, ok := queries[name]
		if !ok {
			t.Errorf("query %s no longer exists: the guard is looking at the wrong name", name)
			continue
		}
		if !regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+ledger_entry\b`).MatchString(body) {
			t.Errorf("query %s does not select from ledger_entry", name)
		}
	}
}

// derivesTheEffectivePurpose matches a query walking a row's purpose corrections
// itself (ADR-033, #628), by either of the two things the walk cannot do without:
//
//   - joining the corrections to the row they correct, as in
//     c.corrects_transaction_id = t.id; and
//   - comparing a correction's leg with the row by direction, as in
//     leg.direction = t.direction, which is how "the leg that carries the target"
//     is found.
//
// Reading a correction's link as a column (tr.corrects_transaction_id AS ...) and
// finding the row a correction corrects (o.id = tr.corrects_transaction_id) are
// not walks, so those pass. As with derivesTheReversal, the walk with its first
// operands swapped (t.id = c.corrects_transaction_id) is textually the legitimate
// o.id = tr.corrects_transaction_id and cannot be told from it; the second
// pattern catches a walk that spells the join that way, and a person reviewing a
// new query has to catch one that avoids both.
var derivesTheEffectivePurpose = regexp.MustCompile(
	`(?is)\bcorrects_transaction_id\s*=\s*\w+\.id\b|\b\w+\.direction\s*=\s*\w+\.direction\b`)

// TestTheEffectivePurposeGuardSeesEveryShapeOfAWalk pins the guard's own
// pattern, as TestTheGuardsSeeEveryShapeOfADerivation does for the other two.
func TestTheEffectivePurposeGuardSeesEveryShapeOfAWalk(t *testing.T) {
	t.Parallel()
	for _, walk := range []string{
		"SELECT leg.purpose_id FROM transfer c JOIN \"transaction\" leg ON leg.transfer_id = c.id WHERE c.corrects_transaction_id = t.id",
		`WHERE c.corrects_transaction_id=t.id`,
		"WHERE c.corrects_transaction_id\n = t.id",
		`where C.CORRECTS_TRANSACTION_ID = T.ID`,
		`HAVING SUM(CASE WHEN leg.direction = t.direction THEN 1 ELSE -1 END) = 1`,
		`HAVING SUM(IIF(leg.direction=t.direction, 1, -1)) = 1`,
		"CASE WHEN leg.direction\n = t.direction THEN 1 ELSE -1 END",
	} {
		if !derivesTheEffectivePurpose.MatchString(walk) {
			t.Errorf("the effective purpose guard misses: %q", walk)
		}
	}
	for _, notWalk := range []string{
		`SELECT t.effective_purpose_id FROM ledger_entry t`,
		`tr.corrects_transaction_id AS transfer_corrects_transaction_id`,
		`LEFT JOIN "transaction" o ON o.fund_id = tr.fund_id AND o.id = tr.corrects_transaction_id`,
		`AND tr.corrects_transaction_id IS NOT NULL`,
		`LEFT JOIN "transaction" tf ON tf.transfer_id = t.transfer_id AND tf.direction = 'out'`,
		`WHEN COALESCE(o.direction, t.direction) = 'in' THEN t.signed_amount`,
		`INSERT INTO transfer (id, fund_id, kind, corrects_transaction_id, reason, created_at)`,
	} {
		if derivesTheEffectivePurpose.MatchString(notWalk) {
			t.Errorf("the effective purpose guard flags something that is not a walk: %q", notWalk)
		}
	}
}

// TestNoQueryWalksTheCorrectionsOutsideTheView is #628's guard: the effective
// pos is defined in 00001_schema.sql's ledger_entry and nowhere else, so
// a new query that wants it selects ledger_entry.effective_purpose_id. One that
// walked the corrections itself could disagree with the correction dialog about
// where a row's money is, and the next correction would then be built from the
// wrong "from" leg and drive a tag negative (ADR-033).
func TestNoQueryWalksTheCorrectionsOutsideTheView(t *testing.T) {
	t.Parallel()
	for name, body := range queryStatements(t) {
		if derivesTheEffectivePurpose.MatchString(body) {
			t.Errorf("query %s walks a row's purpose corrections itself; select ledger_entry.effective_purpose_id", name)
		}
	}
}

// TestTheEffectivePurposeReadsSelectFromTheView holds the other half: the queries
// that answer "where is this row's money now" read ledger_entry, so the guard
// above cannot be satisfied by a query that quietly stopped following
// corrections.
func TestTheEffectivePurposeReadsSelectFromTheView(t *testing.T) {
	t.Parallel()
	queries := queryStatements(t)
	for _, name := range []string{
		"EffectivePurposeForTransaction", // the next correction's "from" leg
		"ListTransactionsPage",           // the correction dialog's current tag and the "corrected" marker
	} {
		body, ok := queries[name]
		if !ok {
			t.Errorf("query %s no longer exists: the guard is looking at the wrong name", name)
			continue
		}
		if !regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+ledger_entry\b`).MatchString(body) ||
			!regexp.MustCompile(`(?i)\beffective_purpose_id\b`).MatchString(body) {
			t.Errorf("query %s does not select effective_purpose_id from ledger_entry", name)
		}
	}
}
