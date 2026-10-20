package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/tz"
)

// #632 through the router: every route that takes a date reads its ceiling from
// the api's injected clock in the FUND's zone, answers a refusal with the one
// typed wire code, and accepts the ceiling itself. The instant is
// lateEveningWIB, 22:30 WIB on 31 August 2026: still the 31st for a WIB or WITA
// fund and already 1 September for a WIT one, so one table proves both the
// clock's wiring (a route that forgot a.now() would answer by the machine's date,
// which is long past these ceilings) and the zone.

// callJSON sends body as JSON to method path.
func callJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling %s %s: %v", method, path, err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader(raw)))
	return rec
}

func mustCall(t *testing.T, r http.Handler, method, path string, body any) map[string]any {
	t.Helper()
	rec := callJSON(t, r, method, path, body)
	if rec.Code/100 != 2 {
		t.Fatalf("%s %s = %d, want 2xx (body: %s)", method, path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out) // a list or an empty body leaves out nil
	return out
}

func idOf(v map[string]any, key string) int64 {
	n, _ := v[key].(float64)
	return int64(n)
}

// windowEnv is one fund with the rows the routes need around the date.
type windowEnv struct {
	r          http.Handler
	cash, bank int64
	main       int64
	member     int64
	tier       int64
	n          int
}

func newWindowEnv(t *testing.T, zone string) *windowEnv {
	t.Helper()
	r := authedRouterAt(t, testStoreDB(t), fixedClock(lateEveningWIB))
	s := setUpFund(t, r)
	setFundZone(t, r, zone)
	e := &windowEnv{r: r, cash: s.Accounts[0].ID, bank: s.Accounts[1].ID, main: s.MainPurposeID}
	joined := "2026-08-01"
	e.member = decodeMember(t, postMember(t, r, memberRequest{Name: "Jane", JoinedOn: &joined}), http.StatusCreated).ID
	e.tier = mustTier(t, r, "Warga", 10_000)
	return e
}

// name is a label no earlier call in this env used.
func (e *windowEnv) name(prefix string) string {
	e.n++
	return fmt.Sprintf("%s %d", prefix, e.n)
}

type windowKind int

const (
	wEntry windowKind = iota
	wPerson
	wDues
	wRate
)

type windowRoute struct {
	name string
	kind windowKind
	send func(t *testing.T, e *windowEnv, date string) *httptest.ResponseRecorder
}

var windowRoutes = []windowRoute{
	{"POST /api/transactions", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/transactions", map[string]any{
			"account_id": e.cash, "purpose_id": e.main, "direction": "in", "amount": 1000, "occurred_on": d})
	}},
	{"POST /api/dues-payments occurred_on", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/dues-payments", map[string]any{
			"account_id": e.cash, "member_id": e.member, "occurred_on": d, "periods": []map[string]any{{"dues_period": "2026-08", "amount": 1000}}})
	}},
	{"POST /api/dues-payments dues_period", wDues, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/dues-payments", map[string]any{
			"account_id": e.cash, "member_id": e.member, "occurred_on": "2026-08-30", "periods": []map[string]any{{"dues_period": d, "amount": 1000}}})
	}},
	{"POST /api/dues-payments/{id}/reversal", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		rec := callJSON(t, e.r, "POST", "/api/dues-payments", map[string]any{
			"account_id": e.cash, "member_id": e.member, "occurred_on": "2026-08-30", "periods": []map[string]any{{"dues_period": "2026-08", "amount": 1000}}})
		var posted []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &posted); err != nil || len(posted) != 1 {
			t.Fatalf("dues payment for the reversal = %d %s", rec.Code, rec.Body.String())
		}
		return callJSON(t, e.r, "POST", fmt.Sprintf("/api/dues-payments/%d/reversal", idOf(posted[0], "id")), map[string]any{"occurred_on": d})
	}},
	{"POST /api/transfers", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/transfers", map[string]any{
			"purpose_id": e.main, "from_account_id": e.cash, "to_account_id": e.bank, "amount": 1000, "occurred_on": d})
	}},
	{"POST /api/purpose-moves", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		mustCall(t, e.r, "POST", "/api/transactions", map[string]any{
			"account_id": e.cash, "purpose_id": e.main, "direction": "in", "amount": 5000, "occurred_on": "2026-08-01"})
		env := mustCall(t, e.r, "POST", "/api/incidentals", map[string]any{"occasion": e.name("Kurban"), "opened_on": "2026-08-01"})
		return callJSON(t, e.r, "POST", "/api/purpose-moves", map[string]any{
			"from_purpose_id": e.main, "to_purpose_id": idOf(env, "purpose_id"), "account_id": e.cash, "amount": 1000, "occurred_on": d})
	}},
	{"POST /api/incidentals", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/incidentals", map[string]any{"occasion": e.name("Kurban"), "opened_on": d})
	}},
	{"POST /api/incidentals/{id}/close", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		env := mustCall(t, e.r, "POST", "/api/incidentals", map[string]any{"occasion": e.name("Kurban"), "opened_on": "2026-08-01"})
		return callJSON(t, e.r, "POST", fmt.Sprintf("/api/incidentals/%d/close", idOf(env, "purpose_id")), map[string]any{"account_id": e.cash, "closed_on": d})
	}},
	{"POST /api/reimbursements", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/reimbursements", map[string]any{"member_id": e.member, "purpose_id": e.main, "amount": 1000, "incurred_on": d})
	}},
	{"PATCH /api/reimbursements/{id} incurred_on", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		claim := mustCall(t, e.r, "POST", "/api/reimbursements", map[string]any{"member_id": e.member, "purpose_id": e.main, "amount": 1000, "incurred_on": "2026-08-01"})
		return callJSON(t, e.r, "PATCH", fmt.Sprintf("/api/reimbursements/%d", idOf(claim, "id")), map[string]any{"incurred_on": d})
	}},
	{"PATCH /api/reimbursements/{id} waived_on", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		claim := mustCall(t, e.r, "POST", "/api/reimbursements", map[string]any{"member_id": e.member, "purpose_id": e.main, "amount": 1000, "incurred_on": "2026-08-01"})
		return callJSON(t, e.r, "PATCH", fmt.Sprintf("/api/reimbursements/%d", idOf(claim, "id")), map[string]any{"waived_on": d})
	}},
	{"POST /api/reimbursements/{id}/settle", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		claim := mustCall(t, e.r, "POST", "/api/reimbursements", map[string]any{"member_id": e.member, "purpose_id": e.main, "amount": 1000, "incurred_on": "2026-08-01"})
		return callJSON(t, e.r, "POST", fmt.Sprintf("/api/reimbursements/%d/settle", idOf(claim, "id")), map[string]any{"account_id": e.cash, "occurred_on": d})
	}},
	{"POST /api/reconciliations fix", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/reconciliations", map[string]any{"counts": []map[string]any{
			{"account_id": e.cash, "actual_amount": 1000, "resolution": "entry_added",
				"fix": map[string]any{"purpose_id": e.main, "direction": "in", "amount": 1000, "occurred_on": d}},
			{"account_id": e.bank, "actual_amount": 0, "resolution": "matched"},
		}})
	}},
	{"POST /api/accounts opening balance", wEntry, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/accounts", map[string]any{
			"kind": "cash", "name": e.name("Laci"), "opening_balance": map[string]any{"amount": 1000, "occurred_on": d}})
	}},
	{"PATCH /api/accounts/{id} inactive_on", wPerson, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "PATCH", fmt.Sprintf("/api/accounts/%d", e.bank), map[string]any{"inactive_on": d})
	}},
	{"POST /api/members joined_on", wPerson, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", "/api/members", map[string]any{"name": e.name("Siti"), "joined_on": d})
	}},
	{"PATCH /api/members/{id} joined_on", wPerson, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "PATCH", fmt.Sprintf("/api/members/%d", e.member), map[string]any{"joined_on": d})
	}},
	{"PATCH /api/members/{id} inactive_on", wPerson, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "PATCH", fmt.Sprintf("/api/members/%d", e.member), map[string]any{"inactive_on": d})
	}},
	{"POST /api/dues-tiers/{id}/rates", wRate, func(t *testing.T, e *windowEnv, d string) *httptest.ResponseRecorder {
		return callJSON(t, e.r, "POST", fmt.Sprintf("/api/dues-tiers/%d/rates", e.tier), map[string]any{"amount": 1000, "effective_from": d})
	}},
}

// The ceilings at lateEveningWIB for each zone, hand-worked: the last accepted
// value and the first refused one.
var windowEdge = map[string]map[windowKind][2]string{
	tz.Jakarta: {
		wEntry: {"2026-08-31", "2026-09-01"}, wPerson: {"2027-08-31", "2027-09-01"}, wDues: {"2027-08", "2027-09"}, wRate: {"2028-08", "2028-09"},
	},
	tz.Jayapura: {
		wEntry: {"2026-09-01", "2026-09-02"}, wPerson: {"2027-09-01", "2027-09-02"}, wDues: {"2027-09", "2027-10"}, wRate: {"2028-09", "2028-10"},
	},
}

func TestEveryDatedRouteHoldsTheCeilingInTheFundsZoneAndAnswersOneCode(t *testing.T) {
	t.Parallel()
	for _, route := range windowRoutes {
		for _, zone := range []string{tz.Jakarta, tz.Jayapura} {
			edge := windowEdge[zone][route.kind]
			t.Run(route.name+"/"+zone, func(t *testing.T) {
				t.Parallel()
				e := newWindowEnv(t, zone)

				rec := route.send(t, e, edge[1])
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("one past the ceiling (%s) = %d, want 400 (body: %s)", edge[1], rec.Code, rec.Body.String())
				}
				if got := decodeError(t, rec); got.Code != "date_out_of_range" {
					t.Errorf("one past the ceiling (%s): code = %q, want date_out_of_range", edge[1], got.Code)
				}

				if rec := route.send(t, e, edge[0]); rec.Code/100 != 2 {
					t.Errorf("the ceiling itself (%s) = %d, want 2xx (body: %s)", edge[0], rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// The floor is the same typed code through the router, a day below it refused
// and the day itself accepted; the schema's CHECK is never what the treasurer
// meets.
func TestEveryDatedRouteHoldsTheFloorAndAnswersOneCode(t *testing.T) {
	t.Parallel()
	for _, route := range windowRoutes {
		floor, below := "2000-01-01", "1999-12-31"
		if route.kind == wDues || route.kind == wRate {
			floor, below = "2000-01", "1999-12"
		}
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			e := newWindowEnv(t, tz.Jakarta)

			rec := route.send(t, e, below)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("one below the floor (%s) = %d, want 400 (body: %s)", below, rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "date_out_of_range" {
				t.Errorf("one below the floor (%s): code = %q, want date_out_of_range", below, got.Code)
			}
			if rec := route.send(t, e, floor); rec.Code/100 != 2 {
				t.Errorf("the floor itself (%s) = %d, want 2xx (body: %s)", floor, rec.Code, rec.Body.String())
			}
		})
	}
}

// POST /api/setup has no fund yet: the opening balance's ceiling reads the zone
// in the body, with the same injected clock. A refused setup leaves no fund.
func TestSetupOpeningBalanceCeilingReadsTheRequestsZone(t *testing.T) {
	t.Parallel()
	for zone, edge := range map[string][2]string{
		tz.Jakarta:  {"2026-08-31", "2026-09-01"},
		tz.Jayapura: {"2026-09-01", "2026-09-02"},
	} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			setup := func(r http.Handler, date string) *httptest.ResponseRecorder {
				return callJSON(t, r, "POST", "/api/setup", map[string]any{
					"name": "Dana", "time_zone": zone,
					"accounts": []map[string]any{{"kind": "cash", "name": "Tunai", "opening_balance": map[string]any{"amount": 5000, "occurred_on": date}}},
				})
			}
			r := authedRouterAt(t, testStoreDB(t), fixedClock(lateEveningWIB))

			rec := setup(r, edge[1])
			if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "date_out_of_range" {
				t.Fatalf("opening balance one past the ceiling (%s) = %d %s, want 400 date_out_of_range", edge[1], rec.Code, rec.Body.String())
			}
			if get := callJSON(t, r, "GET", "/api/fund", nil); get.Code != http.StatusNotFound {
				t.Errorf("after a refused setup GET /api/fund = %d, want 404: no fund may be left behind", get.Code)
			}
			if rec := setup(r, edge[0]); rec.Code != http.StatusCreated {
				t.Errorf("opening balance on the ceiling (%s) = %d, want 201 (body: %s)", edge[0], rec.Code, rec.Body.String())
			}
		})
	}
}
