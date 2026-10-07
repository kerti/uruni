package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The server runs UTC but the treasurer lives in Asia/Jakarta (UTC+7), so the
// last UTC instant of a Jakarta month is 16:59:59Z on its last day and the
// next Jakarta month begins at 17:00:00Z (#379). Every route that reckons a
// calendar day or month must flip on that second, not at UTC midnight.
var (
	beforeSeptemberInJakarta = time.Date(2026, 8, 31, 16, 59, 59, 0, time.UTC)
	atSeptemberInJakarta     = time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)
)

func fixedClock(at time.Time) func() time.Time { return func() time.Time { return at } }

// clockFixture is a fund whose one member joined in August 2026 and owes
// 25000 a month from 2020, with a 30000 rate taking over in September.
// Nothing is paid, so the August period is outstanding exactly when the
// current month is September or later.
func clockFixture(t *testing.T, r http.Handler) memberResponse {
	t.Helper()
	tier := setUpTier(t, r, "Full")
	for _, rate := range []duesRateRequest{
		{Amount: 25_000, EffectiveFrom: "2020-01"},
		{Amount: 30_000, EffectiveFrom: "2026-09"},
	} {
		if rec := postDuesRate(t, r, tier.ID, rate); rec.Code != http.StatusCreated {
			t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
	}
	joined := "2026-08-01"
	rec := postMember(t, r, memberRequest{Name: "Jane", TierID: &tier.ID, JoinedOn: &joined})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var member memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&member); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	return member
}

// GET /api/members/{id}/outstanding-dues with no ?through= walks to the
// current Jakarta month.
func TestGetOutstandingDuesDefaultsToTheJakartaMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		now  time.Time
		want []string
	}{
		{"one second before September in Jakarta", beforeSeptemberInJakarta, []string{"2026-08"}},
		{"the first second of September in Jakarta", atSeptemberInJakarta, []string{"2026-08", "2026-09"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			member := clockFixture(t, r)

			rec := getOutstandingDues(t, r, member.ID, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET .../outstanding-dues = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
			}
			var got []outstandingDuesResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
			}
			var periods []string
			for _, p := range got {
				periods = append(periods, p.Period)
			}
			if strings.Join(periods, ",") != strings.Join(tc.want, ",") {
				t.Errorf("outstanding periods at %s = %v, want %v", tc.now.Format(time.RFC3339), periods, tc.want)
			}
		})
	}
}

// GET /api/members: current_rate and arrears_months both read the Jakarta
// month, from the one value the handler computes per request.
func TestGetMembersReadsTheJakartaMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		now          time.Time
		wantRate     int64
		wantArrears  int
		wantArrearsM string
	}{
		{"one second before September in Jakarta", beforeSeptemberInJakarta, 25_000, 0, "August is still the current month"},
		{"the first second of September in Jakarta", atSeptemberInJakarta, 30_000, 1, "August is now a month behind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			clockFixture(t, r)

			page := decodeMembersPage(t, getMembers(t, r, ""))
			if len(page.Members) != 1 {
				t.Fatalf("GET /api/members = %d members, want 1", len(page.Members))
			}
			got := page.Members[0]
			if got.CurrentRate == nil || *got.CurrentRate != tc.wantRate {
				t.Errorf("current_rate at %s = %v, want %d", tc.now.Format(time.RFC3339), got.CurrentRate, tc.wantRate)
			}
			if got.ArrearsMonths != tc.wantArrears {
				t.Errorf("arrears_months at %s = %d, want %d (%s)", tc.now.Format(time.RFC3339), got.ArrearsMonths, tc.wantArrears, tc.wantArrearsM)
			}
		})
	}
}

// A direct GET /api/backup names the zip with the Jakarta date, not the
// server's: at 17:00:00Z it is already the next day for the treasurer.
func TestDownloadBackupNamesTheZipWithTheJakartaDate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		now  time.Time
		want string
	}{
		{beforeSeptemberInJakarta, `attachment; filename="uruni-2026-08-31.zip"`},
		{atSeptemberInJakarta, `attachment; filename="uruni-2026-09-01.zip"`},
	}
	for _, tc := range cases {
		t.Run(tc.now.Format(time.RFC3339), func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			setUpFund(t, r)

			rec := getBackup(t, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /api/backup = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Disposition"); got != tc.want {
				t.Errorf("Content-Disposition = %q, want %q", got, tc.want)
			}
			if _, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len())); err != nil {
				t.Errorf("body is not a zip: %v", err)
			}
		})
	}
}
