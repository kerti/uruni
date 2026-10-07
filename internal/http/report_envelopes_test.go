package http

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// envelopeScenario is September 2026 with three envelopes:
//
//	Halal bihalal  open, minimum 20.000, for Hadi; Ani gave 25.000,
//	               Budi gave 5.000, Cici nothing, Late (joined after) gave 12.000
//	Santunan       opened and closed in September, Ani gave 10.000
//	Lama           closed in August: not in September's report
type envelopeScenario struct {
	reportFixture
	base         string
	halal, other int64 // purpose ids
}

func newEnvelopeScenario(t *testing.T) envelopeScenario {
	t.Helper()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	q := store.New(f.db)
	mk := func(name string, joined *string) int64 {
		m, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: f.fund.ID, Name: name, JoinedOn: joined, CreatedAt: 1})
		if err != nil {
			t.Fatalf("CreateMember(%s) = %v", name, err)
		}
		return m.ID
	}
	ani, budi, hadi := mk("Ani", nil), mk("Budi", nil), mk("Hadi", nil)
	mk("Cici", nil)
	joined := "2026-09-10"
	late := mk("Late", &joined)

	minimum := money.Amount(20_000)
	halal, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{
		FundID: f.fund.ID, Occasion: "Halal bihalal", OpenedOn: "2026-09-01", MinimumPerMember: &minimum, RecipientMemberIDs: []int64{hadi},
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	give := func(purpose int64, member int64, amount money.Amount, on string) {
		t.Helper()
		if _, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: f.fund.ID, AccountID: f.cashID, PurposeID: purpose, Direction: "in", Amount: amount, OccurredOn: on, MemberID: &member,
		}); err != nil {
			t.Fatalf("PostTransaction() = %v", err)
		}
	}
	give(halal.PurposeID, ani, 25_000, "2026-09-05")
	give(halal.PurposeID, budi, 5_000, "2026-09-06")
	give(halal.PurposeID, late, 12_000, "2026-09-12")

	closeEnv := func(occasion, opened, closed string, gift money.Amount) int64 {
		t.Helper()
		e, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: f.fund.ID, Occasion: occasion, OpenedOn: opened})
		if err != nil {
			t.Fatalf("OpenIncidental(%s) = %v", occasion, err)
		}
		give(e.PurposeID, ani, gift, opened)
		if _, err := f.l.CloseIncidentalAndRoll(ctx, ledger.CloseIncidentalAndRollParams{
			FundID: f.fund.ID, PurposeID: e.PurposeID, AccountID: f.cashID, ClosedOn: closed,
		}); err != nil {
			t.Fatalf("CloseIncidentalAndRoll(%s) = %v", occasion, err)
		}
		return e.PurposeID
	}
	other := closeEnv("Santunan", "2026-09-02", "2026-09-20", 10_000)
	closeEnv("Lama", "2026-07-01", "2026-08-10", 7_000)

	return envelopeScenario{reportFixture: f, base: "/report/" + f.fund.ReportSlug + "?month=2026-09", halal: halal.PurposeID, other: other}
}

func envelopeSection(body string) string {
	i := strings.Index(body, `id="envelopes"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</section>")
	return html.UnescapeString(body[i : i+j])
}

func envelopeNames(sec string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<details class="envelope">\s*<summary>\s*<span class="env-head">\s*<span class="name">([^<]*)</span>`).FindAllStringSubmatch(sec, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestReportEnvelopesListsOpenAndClosedInTheMonthWithSummary(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	rec := s.get(t, s.base)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	sec := envelopeSection(body)
	if got, want := strings.Join(envelopeNames(sec), ","), "Halal bihalal,Santunan"; got != want {
		t.Fatalf("envelopes = %q, want %q (Lama closed in August is absent)", got, want)
	}
	if strings.Contains(sec, "Lama") {
		t.Errorf("section names the envelope closed in an earlier month")
	}
	// After the dues section, as ADR-035 orders them.
	if strings.Index(body, `id="envelopes"`) < strings.Index(body, `id="transactions"`) {
		t.Errorf("envelopes section precedes transactions")
	}
	halal := sec[:strings.Index(sec, "Santunan")]
	for _, want := range []string{
		`<span class="badge open">Berjalan</span>`,
		"Terkumpul: Rp\u00a042.000",
		"2 dari 3 sudah menyumbang", // Ani and Budi of Ani, Budi, Cici
	} {
		if !strings.Contains(halal, want) {
			t.Errorf("open envelope lacks %q:\n%s", want, halal)
		}
	}
	closed := sec[strings.Index(sec, "Santunan"):]
	for _, want := range []string{
		`<span class="badge closed">Ditutup</span>`,
		"Terkumpul: Rp\u00a010.000", // collected, not the zero balance after the roll
		"Ani",
	} {
		if !strings.Contains(closed, want) {
			t.Errorf("closed envelope lacks %q:\n%s", want, closed)
		}
	}
}

func TestReportEnvelopesParticipationInTheAppsWords(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	sec := envelopeSection(s.get(t, s.base).Body.String())
	halal := sec[:strings.Index(sec, "Santunan")]
	for _, want := range []string{
		"Untuk: Hadi",
		`<span class="state sudah">Sudah menyumbang</span>`,
		`<span class="state belum">Belum menyumbang</span>`,
		`<span class="state kurang">Kurang dari minimal</span>`,
		"Rp\u00a025.000",
		"Rp\u00a05.000",
		"Sumbangan lain",
		"Late",
		"Rp\u00a012.000",
	} {
		if !strings.Contains(halal, want) {
			t.Errorf("open envelope lacks %q:\n%s", want, halal)
		}
	}
	// Hadi is a recipient: never expected, so not a row of the table.
	if strings.Contains(regexp.MustCompile(`Untuk: Hadi`).ReplaceAllString(halal, ""), "Hadi") {
		t.Errorf("recipient appears as a participant:\n%s", halal)
	}
	// Late is listed once, under Sumbangan lain, never as "belum".
	if strings.Index(halal, "Late") < strings.Index(halal, "Sumbangan lain") {
		t.Errorf("Late appears before Sumbangan lain")
	}
}

func TestReportEnvelopesKurangIsNeverTerracotta(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	body := s.get(t, s.base).Body.String()
	sec := envelopeSection(body)
	if strings.Contains(sec, "attention") || strings.Contains(sec, "partial") {
		t.Errorf("envelope section uses the attention/terracotta class:\n%s", sec)
	}
	rule := regexp.MustCompile(`\.state\.kurang\{[^}]*\}`).FindString(body)
	if strings.Contains(rule, "attention") {
		t.Errorf(".state.kurang = %q, want a neutral colour", rule)
	}
	if !strings.Contains(body, ".state{") || strings.Contains(regexp.MustCompile(`\.state\{[^}]*\}`).FindString(body), "attention") {
		t.Errorf("base .state rule missing or terracotta")
	}
}

func TestReportEnvelopesAreReadOnly(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	sec := envelopeSection(s.get(t, s.base).Body.String())
	for _, bad := range []string{"<a ", "<a>", "<button", "<form", "<input", "href"} {
		if strings.Contains(sec, bad) {
			t.Errorf("envelope section contains %q", bad)
		}
	}
}

func TestReportEnvelopesPurposeFilter(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	main := s.get(t, s.base+"&purpose="+itoa(s.mainID)).Body.String()
	if envelopeSection(main) != "" {
		t.Errorf("Kas Utama filter still shows the envelopes section")
	}
	one := envelopeSection(s.get(t, s.base+"&purpose="+itoa(s.other)).Body.String())
	if got, want := strings.Join(envelopeNames(one), ","), "Santunan"; got != want {
		t.Errorf("envelope filter = %q, want %q", got, want)
	}
	all := envelopeSection(s.get(t, s.base+"&purpose=999999").Body.String())
	if got := len(envelopeNames(all)); got != 2 {
		t.Errorf("unknown purpose shows %d envelopes, want 2 (no filter)", got)
	}
}

func TestReportEnvelopesHiddenWithNoneAndOldEnvelopeNeverFilteredIn(t *testing.T) {
	t.Parallel()
	s := newEnvelopeScenario(t)
	if sec := envelopeSection(s.get(t, "/report/"+s.fund.ReportSlug+"?month=2026-08").Body.String()); strings.Contains(sec, "Halal") {
		t.Errorf("August shows an envelope opened in September")
	}
	f := newReportFixture(t, "Kas RT 06")
	f.post(t, "in", 10_000, "2026-09-05")
	body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
	if strings.Contains(body, `id="envelopes"`) || strings.Contains(body, "<details") {
		t.Errorf("a fund with no envelopes shows the section")
	}
}
