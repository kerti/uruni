package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ADR-050 over HTTP: POST and DELETE /api/members/{id}/dues-waivers, and what the
// treasurer's dues views then say.

func sendWaiver(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func postDuesWaivers(r http.Handler, memberID int64, body string) *httptest.ResponseRecorder {
	return sendWaiver(r, http.MethodPost, fmt.Sprintf("/api/members/%d/dues-waivers", memberID), body)
}

func deleteDuesWaiverCall(r http.Handler, memberID int64, period string) *httptest.ResponseRecorder {
	return sendWaiver(r, http.MethodDelete, fmt.Sprintf("/api/members/%d/dues-waivers/%s", memberID, period), "")
}

// waiverFixture is a fund on 15 September 2026 with one member, Citra, joined in
// January on Rp25.000 a month, who has paid August in full.
type waiverFixture struct {
	r      http.Handler
	cash   int64
	member int64
}

func newWaiverFixture(t *testing.T) waiverFixture {
	t.Helper()
	r := authedRouterAt(t, testStoreDB(t), fixedClock(time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)))
	setup := setUpFund(t, r)
	tierRec := postDuesTier(t, r, "Rumah")
	if tierRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d (body: %s)", tierRec.Code, tierRec.Body.String())
	}
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatal(err)
	}
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d (body: %s)", rec.Code, rec.Body.String())
	}
	joined := "2026-01-01"
	rec := postMember(t, r, memberRequest{Name: "Citra", TierID: &tier.ID, JoinedOn: &joined})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&member); err != nil {
		t.Fatal(err)
	}
	f := waiverFixture{r: r, cash: setup.CashAccountID(t), member: member.ID}
	if rec := f.pay(t, "2026-08", 25_000); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d (body: %s)", rec.Code, rec.Body.String())
	}
	return f
}

func (f waiverFixture) pay(t *testing.T, period string, amount int64) *httptest.ResponseRecorder {
	t.Helper()
	return postDuesPayment(t, f.r, duesPaymentRequest{
		AccountID: f.cash, MemberID: f.member, OccurredOn: "2026-09-10",
		Periods: []duesPaymentPeriod{{DuesPeriod: period, Amount: amount}},
	})
}

func (f waiverFixture) status(t *testing.T, period string) duesStatusResponse {
	t.Helper()
	rec := getDuesStatus(t, f.r, period)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var rows []duesStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Member.ID == f.member {
			return row
		}
	}
	t.Fatalf("no dues-status row for the member in %s", period)
	return duesStatusResponse{}
}

func TestPostDuesWaiversCreatesThemAndTheDuesViewsReadWaived(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)

	rec := postDuesWaivers(f.r, f.member, `{"periods":["2026-06","2026-07","2026-10"],"note":"  Rumah kebakaran "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST dues-waivers = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created []duesWaiverResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(created) != 3 {
		t.Fatalf("created %d waivers, want 3", len(created))
	}
	for i, period := range []string{"2026-06", "2026-07", "2026-10"} {
		c := created[i]
		if c.DuesPeriod != period || c.MemberID != f.member || c.ID == 0 || c.Note == nil || *c.Note != "Rumah kebakaran" {
			t.Errorf("created[%d] = %+v, want %s for the member with the trimmed note", i, c, period)
		}
	}

	for _, period := range []string{"2026-06", "2026-10"} {
		row := f.status(t, period)
		if row.Status != "waived" || row.WaiverNote == nil || *row.WaiverNote != "Rumah kebakaran" || row.PaidAmount != 0 {
			t.Errorf("dues-status %s = %s note %v paid %d, want waived with the note and nothing paid", period, row.Status, row.WaiverNote, row.PaidAmount)
		}
	}
	if row := f.status(t, "2026-05"); row.Status != "unpaid" || row.WaiverNote != nil {
		t.Errorf("dues-status of an unwaived month = %s note %v, want unpaid and no note", row.Status, row.WaiverNote)
	}

	// The payment form's checklist, through the same walk.
	out := getOutstandingDues(t, f.r, f.member, "2026-10")
	var outstanding []outstandingDuesResponse
	if err := json.NewDecoder(out.Body).Decode(&outstanding); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range outstanding {
		got = append(got, o.Period)
	}
	if want := "2026-01,2026-02,2026-03,2026-04,2026-05,2026-09"; strings.Join(got, ",") != want {
		t.Errorf("outstanding-dues = %v, want %s (waived June, July and October left out)", got, want)
	}

	// The grid.
	grid := f.r
	rec = sendWaiver(grid, http.MethodGet, "/api/dues-status/grid", "")
	var g duesGridResponse
	if err := json.NewDecoder(rec.Body).Decode(&g); err != nil {
		t.Fatal(err)
	}
	if len(g.Members) != 1 {
		t.Fatalf("grid members = %d, want 1", len(g.Members))
	}
	cells := map[string]string{}
	for i, p := range g.Periods {
		cells[p] = g.Members[0].Cells[i]
	}
	if cells["2026-06"] != "waived" || cells["2026-07"] != "waived" || cells["2026-08"] != "paid" || cells["2026-09"] != "unpaid" {
		t.Errorf("grid cells = %v, want June and July waived, August paid, September unpaid", cells)
	}
}

func TestPostDuesWaiversStoresNoNoteAsNull(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)
	rec := postDuesWaivers(f.r, f.member, `{"periods":["2026-06"],"note":"   "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST dues-waivers = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"note":null`) {
		t.Errorf("body = %s, want a null note", rec.Body.String())
	}
	if rec := postDuesWaivers(f.r, f.member, `{"periods":["2026-07"]}`); rec.Code != http.StatusCreated {
		t.Errorf("POST dues-waivers without a note = %d, want 201", rec.Code)
	}
}

func TestPostDuesWaiversRefusesWithStableCodes(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)
	if rec := postDuesWaivers(f.r, f.member, `{"periods":["2026-05"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup waiver = %d", rec.Code)
	}

	for _, tc := range []struct {
		name   string
		member int64
		body   string
		status int
		code   string
		field  string
	}{
		{"a month already waived", f.member, `{"periods":["2026-05"]}`, 409, "dues_already_waived", ""},
		{"a month paid in full", f.member, `{"periods":["2026-08"]}`, 409, "dues_not_owed", ""},
		{"a month before the member joined", f.member, `{"periods":["2025-12"]}`, 409, "dues_not_owed", ""},
		{"one owed and one paid is all or nothing", f.member, `{"periods":["2026-04","2026-08"]}`, 409, "dues_not_owed", ""},
		{"thirteen months ahead", f.member, `{"periods":["2027-10"]}`, 400, "date_out_of_range", ""},
		{"a malformed month", f.member, `{"periods":["2026-13"]}`, 400, "invalid_argument", "periods"},
		{"no months", f.member, `{"periods":[]}`, 400, "invalid_argument", "periods"},
		{"no periods key", f.member, `{}`, 400, "invalid_argument", "periods"},
		{"a repeated month", f.member, `{"periods":["2026-04","2026-04"]}`, 400, "invalid_argument", "periods"},
		{"an unknown field", f.member, `{"periods":["2026-04"],"nope":1}`, 400, "invalid_argument", "nope"},
		{"a member that is none", 9999, `{"periods":["2026-04"]}`, 404, "not_found", ""},
	} {
		rec := postDuesWaivers(f.r, tc.member, tc.body)
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d (body: %s)", tc.name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		got := decodeError(t, rec)
		if got.Code != tc.code || got.Field != tc.field {
			t.Errorf("%s: error = %s field %q, want %s field %q", tc.name, got.Code, got.Field, tc.code, tc.field)
		}
	}

	// A refused batch leaves none of its months behind: April is still owed.
	if row := f.status(t, "2026-04"); row.Status != "unpaid" {
		t.Errorf("April after refused batches = %s, want unpaid", row.Status)
	}

	rec := sendWaiver(f.r, http.MethodPost, "/api/members/abc/dues-waivers", `{"periods":["2026-04"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST with a non-numeric member id = %d, want 400", rec.Code)
	}
}

func TestDeleteDuesWaiverTakesTheMonthBackAndRefusesAMonthNotWaived(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)
	postDuesWaivers(f.r, f.member, `{"periods":["2026-05","2026-06"],"note":"x"}`)

	rec := deleteDuesWaiverCall(f.r, f.member, "2026-05")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("DELETE = %d body %q, want 204 and nothing", rec.Code, rec.Body.String())
	}
	if row := f.status(t, "2026-05"); row.Status != "unpaid" || row.WaiverNote != nil {
		t.Errorf("May after un-waiving = %s note %v, want unpaid and no note", row.Status, row.WaiverNote)
	}
	if row := f.status(t, "2026-06"); row.Status != "waived" {
		t.Errorf("June = %s, want it still waived", row.Status)
	}

	for _, tc := range []struct {
		name, period string
		member       int64
		status       int
		code         string
	}{
		{"the month just undone", "2026-05", f.member, 409, "dues_not_waived"},
		{"a month never waived", "2026-04", f.member, 409, "dues_not_waived"},
		{"a malformed month", "2026-5", f.member, 400, "invalid_argument"},
		{"a member that is none", "2026-06", 9999, 404, "not_found"},
	} {
		rec := deleteDuesWaiverCall(f.r, tc.member, tc.period)
		if rec.Code != tc.status {
			t.Errorf("%s: DELETE = %d, want %d (body: %s)", tc.name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		if got := decodeError(t, rec); got.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, got.Code, tc.code)
		}
	}
}

func TestADuesPaymentForAWaivedMonthIsRefusedByCode(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)
	postDuesWaivers(f.r, f.member, `{"periods":["2026-05"]}`)

	rec := f.pay(t, "2026-05", 25_000)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/dues-payments for a waived month = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "dues_period_waived" {
		t.Errorf("code = %q, want dues_period_waived", got.Code)
	}
}

func TestWaivingAndUnwaivingMoveNoMoneyOverHTTP(t *testing.T) {
	t.Parallel()
	f := newWaiverFixture(t)
	before := getBalances(t, f.r).Body.String()

	postDuesWaivers(f.r, f.member, `{"periods":["2026-05","2026-06"],"note":"x"}`)
	if got := getBalances(t, f.r).Body.String(); got != before {
		t.Errorf("balances after waiving = %s, want %s", got, before)
	}
	deleteDuesWaiverCall(f.r, f.member, "2026-05")
	if got := getBalances(t, f.r).Body.String(); got != before {
		t.Errorf("balances after un-waiving = %s, want %s", got, before)
	}
}
