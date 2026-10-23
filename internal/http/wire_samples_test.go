package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWireSamples writes one real response per shape the web app reads into
// web/src/test/wire (#524), from a fund built the same way every run, so
// web/src/test/wire.test.ts can hold each TypeScript interface to the JSON the
// server actually sends. A Go field renamed or added without the web side
// following fails there, or here when the sample was not regenerated.
//
// Determinism: the clock is fixed, ids come from a fresh database, and the two
// values a fixed clock does not pin - created_at (the wall clock) and the
// report slug (random) - are replaced by fixed ones.
func TestWireSamples(t *testing.T) {
	t.Parallel()
	r := authedRouterAt(t, testStoreDB(t), fixedClock(time.Date(2026, 9, 15, 5, 0, 0, 0, time.UTC)))
	w := wireFixture{t: t, r: r}

	var setup setupResponse
	w.decode(w.send(http.MethodPost, "/api/setup", `{"name":"Kas RT 05","time_zone":"Asia/Jakarta","accounts":[
		{"kind":"cash","name":"Tunai","opening_balance":{"amount":500000,"occurred_on":"2026-08-01","note":"Saldo awal"}},
		{"kind":"bank","name":"Bank"}]}`, "setup"), &setup)
	main, cash, bank := setup.MainPurposeID, setup.Accounts[0].ID, setup.Accounts[1].ID

	tier := w.id(w.send(http.MethodPost, "/api/dues-tiers", `{"name":"Rumah"}`, ""))
	w.send(http.MethodPost, fmt.Sprintf("/api/dues-tiers/%d/rates", tier), `{"amount":50000,"effective_from":"2026-01"}`, "")
	budi := w.id(w.send(http.MethodPost, "/api/members", fmt.Sprintf(`{"name":"Budi","tier_id":%d,"joined_on":"2026-01-01"}`, tier), ""))
	sari := w.id(w.send(http.MethodPost, "/api/members", `{"name":"Sari","tier_id":null,"joined_on":"2026-02-01"}`, ""))
	w.send(http.MethodPost, "/api/dues-payments", fmt.Sprintf(`{"account_id":%d,"member_id":%d,"occurred_on":"2026-09-05","note":null,
		"periods":[{"dues_period":"2026-08","amount":50000},{"dues_period":"2026-09","amount":50000}]}`, cash, budi), "")
	// Citra joins in March (January and February are no obligation for her) and
	// part-pays July, so the grid sample carries all four cell states.
	citra := w.id(w.send(http.MethodPost, "/api/members", fmt.Sprintf(`{"name":"Citra","tier_id":%d,"joined_on":"2026-03-01"}`, tier), ""))
	w.send(http.MethodPost, "/api/dues-payments", fmt.Sprintf(`{"account_id":%d,"member_id":%d,"occurred_on":"2026-07-05","note":null,
		"periods":[{"dues_period":"2026-07","amount":20000}]}`, cash, citra), "")

	lamp := w.id(w.send(http.MethodPost, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"out",
		"amount":75000,"occurred_on":"2026-09-02","note":"Beli lampu","is_adjustment":false,"member_id":null}`, cash, main), ""))
	titipan := w.id(w.send(http.MethodPost, "/api/pass-through-purposes", `{"name":"Titipan sumbangan"}`, ""))
	w.send(http.MethodPost, "/api/transfers", fmt.Sprintf(`{"purpose_id":%d,"from_account_id":%d,"to_account_id":%d,
		"amount":100000,"occurred_on":"2026-09-03","note":null}`, main, cash, bank), "transfer")
	w.send(http.MethodPost, fmt.Sprintf("/api/transactions/%d/purpose-correction", lamp), fmt.Sprintf(`{"purpose_id":%d}`, titipan), "")
	slip := w.id(w.send(http.MethodPost, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in",
		"amount":10000,"occurred_on":"2026-09-04","note":"Salah catat","is_adjustment":false,"member_id":null}`, cash, main), ""))
	w.send(http.MethodPost, fmt.Sprintf("/api/transactions/%d/reversal", slip), `{"note":"Batal"}`, "")

	envelope := w.decodeMap(w.send(http.MethodPost, "/api/incidentals", fmt.Sprintf(`{"occasion":"Halal bihalal","target_amount":300000,
		"opened_on":"2026-09-01","minimum_per_member":20000,"recipient_member_ids":[%d]}`, sari), ""))
	envelopeID := int64(envelope["purpose_id"].(float64))
	w.send(http.MethodPost, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in",
		"amount":50000,"occurred_on":"2026-09-06","note":null,"is_adjustment":false,"member_id":%d}`, cash, envelopeID, budi), "")
	w.send(http.MethodPost, "/api/reimbursements", fmt.Sprintf(`{"member_id":%d,"purpose_id":%d,"amount":30000,
		"incurred_on":"2026-09-07","note":"Beli kabel"}`, sari, main), "")

	var balances struct {
		Accounts []struct {
			ID      int64 `json:"id"`
			Balance int64 `json:"balance"`
		} `json:"accounts"`
	}
	w.decode(w.send(http.MethodGet, "/api/balances", "", "balances"), &balances)
	counts := make([]string, 0, len(balances.Accounts))
	// The bank is counted short and left open, so open-lines has a row.
	for _, a := range balances.Accounts {
		if a.ID == bank {
			counts = append(counts, fmt.Sprintf(`{"account_id":%d,"actual_amount":%d,"resolution":"left_open","fix":null}`, a.ID, a.Balance-1000))
			continue
		}
		counts = append(counts, fmt.Sprintf(`{"account_id":%d,"actual_amount":%d,"resolution":"matched","fix":null}`, a.ID, a.Balance))
	}
	reconciliation := w.id(w.send(http.MethodPost, "/api/reconciliations",
		fmt.Sprintf(`{"note":"Hitung September","counts":[%s]}`, strings.Join(counts, ",")), ""))
	w.send(http.MethodPost, "/api/transactions", fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in",
		"amount":5000,"occurred_on":"2026-09-10","note":"Setelah cek kas","is_adjustment":false,"member_id":null}`, cash, main), "")

	for _, read := range []struct{ path, sample string }{
		{"/api/session", "session"},
		{"/api/user", "user"},
		{"/api/fund", "fund"},
		{"/api/accounts", "accounts"},
		{"/api/purposes", "purposes"},
		{"/api/members", "members-page"},
		{"/api/dues-tiers", "dues-tiers"},
		{"/api/transactions", "transactions-page"},
		{"/api/dues-payments", "dues-payments-page"},
		{"/api/dues-status?period=2026-09", "dues-status"},
		{"/api/dues-status/grid", "dues-status-grid"},
		{fmt.Sprintf("/api/members/%d/outstanding-dues", budi), "outstanding-dues"},
		{"/api/incidentals", "incidentals"},
		{fmt.Sprintf("/api/incidentals/%d", envelopeID), "incidental"},
		{fmt.Sprintf("/api/incidentals/%d/participation", envelopeID), "incidental-participation"},
		{"/api/reimbursements", "reimbursements-page"},
		{"/api/reconciliations", "reconciliations-page"},
		{"/api/reconciliations/latest", "reconciliation-latest"},
		{fmt.Sprintf("/api/reconciliations/%d", reconciliation), "reconciliation"},
		{"/api/reconciliations/open-lines", "reconciliation-open-lines"},
	} {
		w.send(http.MethodGet, read.path, "", read.sample)
	}

	// The error envelope, with the field #530 added.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pass-through-purposes", strings.NewReader(`{"nama":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	w.write("error", rec.Body.Bytes())
}

type wireFixture struct {
	t *testing.T
	r http.Handler
}

// send makes one request, fails on anything but a 2xx, and writes the body as
// the named sample unless sample is "".
func (w wireFixture) send(method, path, body, sample string) []byte {
	w.t.Helper()
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	if rec.Code < 200 || rec.Code > 299 {
		w.t.Fatalf("%s %s = %d (body: %s)", method, path, rec.Code, rec.Body.String())
	}
	if sample != "" {
		w.write(sample, rec.Body.Bytes())
	}
	return rec.Body.Bytes()
}

func (w wireFixture) decode(body []byte, dst any) {
	w.t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		w.t.Fatalf("decoding %s: %v", body, err)
	}
}

func (w wireFixture) decodeMap(body []byte) map[string]any {
	w.t.Helper()
	var m map[string]any
	w.decode(body, &m)
	return m
}

func (w wireFixture) id(body []byte) int64 {
	w.t.Helper()
	id, ok := w.decodeMap(body)["id"].(float64)
	if !ok {
		w.t.Fatalf("no id in %s", body)
	}
	return int64(id)
}

// write normalises the body and checks it against its sample file.
func (w wireFixture) write(sample string, body []byte) {
	w.t.Helper()
	var v any
	w.decode(body, &v)
	pinned := pinUnfixedValues(v)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(pinned); err != nil {
		w.t.Fatal(err)
	}
	checkGolden(w.t, filepath.Join(wireDir, sample+".json"), out.Bytes())
}

// pinUnfixedValues replaces what a fixed clock does not fix: every *_at
// timestamp (written from the wall clock) and the random report slug.
func pinUnfixedValues(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for key, value := range v {
			switch {
			case strings.HasSuffix(key, "_at"):
				if _, isNumber := value.(float64); isNumber {
					v[key] = 1789444800
				}
			case key == "report_slug":
				v[key] = "report-slug"
			case key == "report_url":
				v[key] = "/report/report-slug"
			default:
				v[key] = pinUnfixedValues(value)
			}
		}
	case []any:
		for i, item := range v {
			v[i] = pinUnfixedValues(item)
		}
	}
	return v
}
