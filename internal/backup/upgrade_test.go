package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// v1Of renders a current Document in format 1's shape: members carry the tier
// they are in now (the tier of their latest standing month), and there is no
// member_tiers array. It is the test-side inverse of upgradeV1, close enough to
// build a v1 file from the fixtures that exist.
func v1Of(doc Document) documentV1 {
	current := map[int64]*int64{}
	latestFrom := map[int64]string{}
	latestCorrection := map[int64]int64{}
	for _, mt := range doc.MemberTiers {
		from, c := latestFrom[mt.MemberID], latestCorrection[mt.MemberID]
		if _, seen := latestFrom[mt.MemberID]; !seen || mt.EffectiveFrom > from || (mt.EffectiveFrom == from && mt.Correction > c) {
			latestFrom[mt.MemberID], latestCorrection[mt.MemberID] = mt.EffectiveFrom, mt.Correction
			current[mt.MemberID] = mt.TierID
		}
	}
	members := make([]memberV1, 0, len(doc.Members))
	for _, m := range doc.Members {
		joined := m.JoinedOn
		members = append(members, memberV1{
			ID: m.ID, FundID: m.FundID, Name: m.Name, TierID: current[m.ID],
			JoinedOn: &joined, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
		})
	}
	// A v1 file holds one row per tier and month and no deletions: the rates in
	// force now, which is what the standing rows of v2 say.
	standingRate := map[[2]string]DuesRate{}
	for _, r := range doc.DuesRates {
		key := [2]string{strconv.FormatInt(r.TierID, 10), r.EffectiveFrom}
		if cur, ok := standingRate[key]; !ok || r.Correction > cur.Correction {
			standingRate[key] = r
		}
	}
	rates := make([]rateV1, 0, len(standingRate))
	for _, r := range doc.DuesRates {
		if s := standingRate[[2]string{strconv.FormatInt(r.TierID, 10), r.EffectiveFrom}]; s.ID == r.ID && !r.Deleted {
			rates = append(rates, rateV1{ID: r.ID, TierID: r.TierID, Amount: r.Amount, EffectiveFrom: r.EffectiveFrom, CreatedAt: r.CreatedAt})
		}
	}
	return documentV1{
		FormatVersion: 1,
		Users:         doc.Users, Funds: doc.Funds, Accounts: doc.Accounts, Purposes: doc.Purposes,
		DuesTiers: doc.DuesTiers, DuesRates: rates, Members: members,
		Transfers: doc.Transfers, Reimbursements: doc.Reimbursements, Transactions: doc.Transactions, Receipts: doc.Receipts,
		Reconciliations: doc.Reconciliations, ReconciliationLines: doc.ReconciliationLines,
		Incidentals: doc.Incidentals, IncidentalRecipients: doc.IncidentalRecipients,
		Totals: doc.Totals,
	}
}

func v1Zip(t *testing.T, v1 documentV1) []byte {
	t.Helper()
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatalf("marshaling a v1 document: %v", err)
	}
	return zipOf(t, raw, nil)
}

func TestCanUpgradeFormat(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		version int64
		want    bool
	}{
		{-1, false}, {0, false}, {1, true}, {FormatVersion, true}, {FormatVersion + 1, false},
	} {
		if got := CanUpgradeFormat(tt.version); got != tt.want {
			t.Errorf("CanUpgradeFormat(%d) = %v, want %v", tt.version, got, tt.want)
		}
	}
	if FormatVersion != 2 {
		t.Errorf("FormatVersion = %d, want 2 (ADR-040 bumps it once for the epic)", FormatVersion)
	}
}

func TestParseUploadUpgradesAVersion1FileToTheCurrentShape(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, doc := exportFixture(t, srcDB, t.TempDir())

	v1 := v1Of(doc)
	parsed, err := parseUploadBytes(t, v1Zip(t, v1))
	if err != nil {
		t.Fatalf("ParseUpload(v1) = %v, want no error", err)
	}
	got := parsed.Document
	if got.FormatVersion != FormatVersion {
		t.Errorf("upgraded FormatVersion = %d, want %d", got.FormatVersion, FormatVersion)
	}

	// Andi is in the fixture's second tier now (v1 holds only the current tier), and
	// Budi in none: one row, from the month of Andi's join date, correction 0,
	// created_at taken from the member's own, id 1.
	var andi Member
	for _, m := range got.Members {
		if m.Name == "Andi" {
			andi = m
		}
	}
	if len(got.MemberTiers) != 1 {
		t.Fatalf("upgraded member_tiers = %+v, want exactly one row (Budi has no tier)", got.MemberTiers)
	}
	row := got.MemberTiers[0]
	if row.ID != 1 || row.MemberID != andi.ID || row.FundID != andi.FundID || row.Correction != 0 ||
		row.EffectiveFrom != andi.JoinedOn[:7] || row.CreatedAt != andi.CreatedAt || row.TierID == nil {
		t.Errorf("backfilled row = %+v, want id 1, Andi (%d), correction 0, from %s, created_at %d, with a tier",
			row, andi.ID, andi.JoinedOn[:7], andi.CreatedAt)
	}

	// Everything but the members and their tiers carries over untouched, totals
	// included: no ledger row moves.
	if !slices.Equal(got.Totals.Funds, doc.Totals.Funds) || !slices.Equal(got.Totals.Accounts, doc.Totals.Accounts) || !slices.Equal(got.Totals.Purposes, doc.Totals.Purposes) {
		t.Errorf("upgraded totals = %+v, want the file's own %+v", got.Totals, doc.Totals)
	}
	if len(got.Transactions) != len(doc.Transactions) || len(got.DuesTiers) != len(doc.DuesTiers) {
		t.Errorf("the upgrade changed a table it should not touch")
	}

	// A v1 file holds the rates in force and nothing else (the fixture's v2 trail,
	// a corrected January and a deleted June, is not in it), and every one becomes
	// correction 0, not deleted.
	if len(v1.DuesRates) != 1 || len(got.DuesRates) != len(v1.DuesRates) {
		t.Fatalf("v1 rates = %+v, upgraded rates = %+v, want the same single standing rate", v1.DuesRates, got.DuesRates)
	}
	if r, in := got.DuesRates[0], v1.DuesRates[0]; r.ID != in.ID || r.TierID != in.TierID || r.Amount != in.Amount ||
		r.EffectiveFrom != in.EffectiveFrom || r.CreatedAt != in.CreatedAt || r.Correction != 0 || r.Deleted {
		t.Errorf("upgraded rate = %+v, want %+v as correction 0, not deleted", r, in)
	}
}

// Ids of the backfilled rows follow member ids, whatever order the file's
// members array is in (CLAUDE.md: no primary-key order).
func TestUpgradeV1NumbersTierRowsInMemberIDOrder(t *testing.T) {
	t.Parallel()
	one, two := int64(1), int64(2)
	v1 := documentV1{
		FormatVersion: 1,
		Funds:         []Fund{{ID: 1, Name: "Dana", Currency: "IDR", ReportSlug: "0000000000000000000slug01", CreatedAt: 1}},
		Members: []memberV1{
			{ID: 9, FundID: 1, Name: "Sembilan", TierID: &two, JoinedOn: strPtr("2026-05-02"), CreatedAt: 90},
			{ID: 3, FundID: 1, Name: "Tiga", TierID: &one, JoinedOn: strPtr("2026-02-10"), CreatedAt: 30},
			{ID: 5, FundID: 1, Name: "Lima", JoinedOn: strPtr("2026-01-01"), CreatedAt: 50},
		},
	}
	doc, err := upgradeV1(v1)
	if err != nil {
		t.Fatalf("upgradeV1() = %v, want no error", err)
	}
	want := []MemberTier{
		{ID: 1, FundID: 1, MemberID: 3, TierID: &one, EffectiveFrom: "2026-02", CreatedAt: 30},
		{ID: 2, FundID: 1, MemberID: 9, TierID: &two, EffectiveFrom: "2026-05", CreatedAt: 90},
	}
	if len(doc.MemberTiers) != len(want) {
		t.Fatalf("member_tiers = %+v, want %+v", doc.MemberTiers, want)
	}
	for i, w := range want {
		g := doc.MemberTiers[i]
		if g.ID != w.ID || g.MemberID != w.MemberID || g.EffectiveFrom != w.EffectiveFrom || g.CreatedAt != w.CreatedAt || *g.TierID != *w.TierID {
			t.Errorf("member_tiers[%d] = %+v, want %+v", i, g, w)
		}
	}
	if len(doc.Members) != 3 || doc.Members[0].ID != 9 {
		t.Errorf("members = %+v, want the file's three in the file's order", doc.Members)
	}
}

func TestUpgradeV1RefusesAMemberJoinedOnThatIsNotADate(t *testing.T) {
	t.Parallel()
	v1 := documentV1{
		FormatVersion: 1,
		Funds:         []Fund{{ID: 1, CreatedAt: 1}},
		Members:       []memberV1{{ID: 1, FundID: 1, Name: "Jane", JoinedOn: strPtr("2026-02-30")}},
	}
	if _, err := upgradeV1(v1); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("upgradeV1(joined_on 2026-02-30) = %v, want ErrMalformedBackup", err)
	}
}

func TestParseUploadRefusesAV2FileThatStillCarriesATierOnItsMembers(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, doc := exportFixture(t, srcDB, t.TempDir())
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	// A v2 file is read strictly: members[].tier_id is gone in it, so it is an
	// unknown field, not a quietly ignored one.
	mangled := strings.Replace(string(raw), `"name":"Andi"`, `"name":"Andi","tier_id":1`, 1)
	if mangled == string(raw) {
		t.Fatal("test bug: the member was not found to mangle")
	}
	if _, err := parseUploadBytes(t, zipOf(t, []byte(mangled), nil)); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload(v2 with members[].tier_id) = %v, want ErrMalformedBackup", err)
	}
}

func TestParseUploadRefusesAVersion1FileWithAnUnknownField(t *testing.T) {
	t.Parallel()
	raw := `{"format_version":1,"members":[],"member_tiers":[]}` // member_tiers is v2's
	if _, err := parseUploadBytes(t, zipOf(t, []byte(raw), nil)); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload(v1 with member_tiers) = %v, want ErrMalformedBackup", err)
	}
}

func TestParseUploadSplitsOlderAndNewerVersions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		raw  string
		want error
	}{
		{`{"format_version":0}`, ErrFormatVersionOlder},
		{`{}`, ErrFormatVersionOlder},
		{`{"format_version":3}`, ErrFormatVersionNewer},
	} {
		_, err := parseUploadBytes(t, zipOf(t, []byte(tt.raw), nil))
		if !errors.Is(err, tt.want) {
			t.Errorf("ParseUpload(%s) = %v, want %v", tt.raw, err, tt.want)
		}
	}
}

// A v1 file whose totals block disagrees with its rows is refused at inspect
// exactly as a v2 one is: the upgrade does not launder a self-contradicting file.
func TestParseUploadRefusesAVersion1FileWhoseTotalsDisagree(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, doc := exportFixture(t, srcDB, t.TempDir())
	v1 := v1Of(doc)
	v1.Totals.Funds = slices.Clone(v1.Totals.Funds)
	v1.Totals.Funds[0].Balance++

	if _, err := parseUploadBytes(t, v1Zip(t, v1)); !errors.Is(err, ErrTotalsMismatch) {
		t.Errorf("ParseUpload(v1, totals off by one) = %v, want ErrTotalsMismatch", err)
	}
}

// The round trip keeps the trail (the golden fixture's member has a superseded
// row), and restore never drops the member_tier trigger: it only inserts into
// and clears the table, and an UPDATE trigger does not fire for either, so
// immutableTriggers stays as it was (ADR-040).
func TestRestoreKeepsSupersededTierRowsAndTheirUpdateTrigger(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
	zipBytes, wantDoc := exportFixture(t, srcDB, srcUploads)

	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	if len(wantDoc.MemberTiers) != 3 {
		t.Fatalf("fixture exports %d member_tier rows, want 3 (one superseded)", len(wantDoc.MemberTiers))
	}
	gotDoc, _, err := BuildDocument(t.Context(), destDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)

	_, err = destDB.Exec(`UPDATE member_tier SET correction = correction + 10 WHERE id = 1`)
	if err == nil || !strings.Contains(err.Error(), "member_tier rows are immutable") {
		t.Errorf("UPDATE member_tier after a restore = %v, want the immutability trigger to refuse", err)
	}
	for _, name := range immutableTriggers {
		if strings.HasPrefix(name, "member_tier") {
			t.Errorf("immutableTriggers names %q: restore only inserts and deletes member_tier, so it must not drop its trigger", name)
		}
	}
}

// A v1 member whose join date, or the adoption date a null one falls back to, is
// before 2000-01-01 cannot be backfilled: member_tier's start month has that
// floor, and #632 gives member.joined_on the same one.
func TestUpgradeV1RefusesAMemberJoinedBefore2000(t *testing.T) {
	t.Parallel()
	one := int64(1)
	fund := Fund{ID: 1, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()}

	explicit := documentV1{FormatVersion: 1, Funds: []Fund{fund},
		Members: []memberV1{{ID: 1, FundID: 1, Name: "Kuno", TierID: &one, JoinedOn: strPtr("1999-12-31")}}}
	if _, err := upgradeV1(explicit); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("upgradeV1(joined_on 1999-12-31) = %v, want ErrMalformedBackup", err)
	}

	// The adoption fallback: a fund with no opening row dates from its creation day.
	old := fund
	old.CreatedAt = time.Date(1999, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	fallback := documentV1{FormatVersion: 1, Funds: []Fund{old},
		Members: []memberV1{{ID: 1, FundID: 1, Name: "Tanpa tanggal", TierID: &one, JoinedOn: nil}}}
	if _, err := upgradeV1(fallback); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("upgradeV1(null joined_on, adoption 1999-06-01) = %v, want ErrMalformedBackup", err)
	}

	ok := documentV1{FormatVersion: 1, Funds: []Fund{fund},
		Members: []memberV1{{ID: 1, FundID: 1, Name: "Tepat", TierID: &one, JoinedOn: strPtr("2000-01-01")}}}
	if _, err := upgradeV1(ok); err != nil {
		t.Errorf("upgradeV1(joined_on 2000-01-01) = %v, want no error", err)
	}
}

// The neutrality fixture's fund has an opening row, so its null joined_on takes
// that day. A fund with none dates from its creation day in Asia/Jakarta (17:00Z
// is already the next day there), and the backfilled tier row follows it - one
// per fund, so a second fund's opening does not leak into the first.
func TestUpgradeV1AdoptionFallsBackToTheFundsCreationDayWhenItHasNoOpening(t *testing.T) {
	t.Parallel()
	one := int64(1)
	created := time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC).Unix() // 2026-09-01 in Jakarta
	v1 := documentV1{
		FormatVersion: 1,
		Funds:         []Fund{{ID: 1, CreatedAt: created}, {ID: 2, CreatedAt: created}},
		Members: []memberV1{
			{ID: 1, FundID: 1, Name: "Tanpa pembukaan", TierID: &one, JoinedOn: nil, CreatedAt: 5},
			{ID: 2, FundID: 2, Name: "Dengan pembukaan", TierID: &one, JoinedOn: nil, CreatedAt: 6},
		},
		Transactions: []Transaction{{ID: 1, FundID: 2, AccountID: 1, PurposeID: 1, Direction: "in", Amount: 1, OccurredOn: "2025-11-20", Kind: "opening"}},
	}
	doc, err := upgradeV1(v1)
	if err != nil {
		t.Fatalf("upgradeV1() = %v, want no error", err)
	}
	if doc.Members[0].JoinedOn != "2026-09-01" || doc.Members[1].JoinedOn != "2025-11-20" {
		t.Errorf("joined_on = %q, %q; want the creation day 2026-09-01 and the opening day 2025-11-20", doc.Members[0].JoinedOn, doc.Members[1].JoinedOn)
	}
	if len(doc.MemberTiers) != 2 || doc.MemberTiers[0].EffectiveFrom != "2026-09" || doc.MemberTiers[1].EffectiveFrom != "2025-11" {
		t.Errorf("member_tiers = %+v, want rows from 2026-09 and 2025-11", doc.MemberTiers)
	}
}
