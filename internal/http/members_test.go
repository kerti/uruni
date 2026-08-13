package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postMember(t *testing.T, r http.Handler, req memberRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling member request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/members", bytes.NewReader(body)))
	return rec
}

func TestPostMembersRequiresAFund(t *testing.T) {
	// No POST /api/setup yet - there is no fund to post a member against.
	rec := postMember(t, testRouter(t), memberRequest{Name: "Jane"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/members before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostMembersCreatesAndListReturnsIt(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	joined := "2026-01-15"
	rec := postMember(t, r, memberRequest{Name: "Jane", JoinedOn: &joined})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var created memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("member.id is zero")
	}
	if created.Name != "Jane" {
		t.Errorf("member.name = %q, want %q", created.Name, "Jane")
	}
	if created.JoinedOn == nil || *created.JoinedOn != joined {
		t.Errorf("member.joined_on = %v, want %q", created.JoinedOn, joined)
	}
	if created.TierID != nil {
		t.Errorf("member.tier_id = %v, want nil", created.TierID)
	}
	if created.InactiveOn != nil {
		t.Errorf("member.inactive_on = %v, want nil (creation cannot set it)", created.InactiveOn)
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/members = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}

	var members []memberResponse
	if err := json.NewDecoder(list.Body).Decode(&members); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	// *string fields compare by address, not value, so this can't use != on
	// the struct - compare the JSON each side encodes to instead.
	gotJSON, _ := json.Marshal(members)
	wantJSON, _ := json.Marshal([]memberResponse{created})
	if len(members) != 1 || string(gotJSON) != string(wantJSON) {
		t.Errorf("GET /api/members = %s, want %s", gotJSON, wantJSON)
	}
}

func TestGetMembersReturnsAnEmptyListBeforeAnyMemberExists(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/members", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/members = %d, want %d", rec.Code, http.StatusOK)
	}
	// [] on the wire, never null, so a client need not nil-check before ranging.
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("GET /api/members body = %q, want %q", got, "[]\n")
	}
}

func TestPostMembersRejectsAnEmptyName(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	// member.name CHECK (length(trim(name)) > 0) - proven through the shared
	// mapper, not a hand-rolled check in the handler.
	rec := postMember(t, r, memberRequest{Name: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with empty name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestPostMembersRejectsATierIDBelongingToNoFund(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	noSuchTier := int64(999)
	rec := postMember(t, r, memberRequest{Name: "Jane", TierID: &noSuchTier})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with a bad tier_id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostMembersRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/members", bytes.NewReader([]byte("{oops"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/members with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetMembersRequiresAFund(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/members", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/members before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}
