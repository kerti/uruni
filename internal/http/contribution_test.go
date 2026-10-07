package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-034: a contribution may name its member, an envelope gains a minimum
// and its recipients, and participation is derived and read back through
// its own route. These tests are the HTTP-layer half of the engine slice -
// the same thin-mapping shape every other handler test in this package
// already exercises.

func createMemberFor(t *testing.T, r http.Handler, name string) memberResponse {
	t.Helper()
	rec := postMember(t, r, memberRequest{Name: name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding member response: %v", err)
	}
	return created
}

func patchIncidentalParticipation(t *testing.T, r http.Handler, purposeID int64, req updateIncidentalParticipationRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling participation request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d", purposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body)))
	return rec
}

func getIncidentalParticipation(t *testing.T, r http.Handler, purposeID int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/incidentals/%d/participation", purposeID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestPostTransactionAcceptsAMemberOnAContribution: member_id round-trips
// through POST /api/transactions and back out through GET /api/transactions,
// on an incidental purpose.
func TestPostTransactionAcceptsAMemberOnAContribution(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")
	member := createMemberFor(t, r, "Jane")

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &member.ID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var posted transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&posted); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if posted.MemberID == nil || *posted.MemberID != member.ID {
		t.Errorf("member_id = %v, want %d", posted.MemberID, member.ID)
	}

	rows := decodeTransactionsList(t, r)
	found := findTransactionByKind(t, rows, "normal")
	if found.MemberID == nil || *found.MemberID != member.ID {
		t.Errorf("listed row's member_id = %v, want %d", found.MemberID, member.ID)
	}
}

// TestPostTransactionRefusesAMemberOutsideAnIncidentalPurpose: mapped to
// 400 invalid_argument, a friendly error rather than the trigger's raw
// message.
func TestPostTransactionRefusesAMemberOutsideAnIncidentalPurpose(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	member := createMemberFor(t, r, "Jane")

	rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &member.ID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/transactions(member, main purpose) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestOpenIncidentalAcceptsMinimumAndRecipients: both ride the create
// request and come back on the create response (minimum) and the detail
// response (recipients).
func TestOpenIncidentalAcceptsMinimumAndRecipients(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	recipient := createMemberFor(t, r, "The Recipient")

	minimum := int64(20_000)
	rec := postIncidental(t, r, openIncidentalRequest{
		Occasion: "Sunatan", OpenedOn: "2026-08-01",
		MinimumPerMember: &minimum, RecipientMemberIDs: []int64{recipient.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/incidentals = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	created := decodeIncidental(t, rec)
	if created.MinimumPerMember == nil || *created.MinimumPerMember != 20_000 {
		t.Errorf("minimum_per_member = %v, want 20000", created.MinimumPerMember)
	}

	detailRec := getIncidentalDetail(t, r, created.PurposeID)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET /api/incidentals/{id} = %d, want %d (body: %s)", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
	var detail incidentalDetailResponse
	if err := json.NewDecoder(detailRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding detail response: %v", err)
	}
	if len(detail.Recipients) != 1 || detail.Recipients[0].MemberID != recipient.ID {
		t.Errorf("recipients = %+v, want exactly [%d]", detail.Recipients, recipient.ID)
	}
}

// TestPatchIncidentalParticipationReplacesMinimumAndRecipients: PATCH
// /api/incidentals/{purposeID} fully replaces both facets, never adds to
// them.
func TestPatchIncidentalParticipationReplacesMinimumAndRecipients(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")
	recipient := createMemberFor(t, r, "The Recipient")

	newMinimum := int64(30_000)
	rec := patchIncidentalParticipation(t, r, envelope.PurposeID, updateIncidentalParticipationRequest{
		MinimumPerMember: &newMinimum, RecipientMemberIDs: []int64{recipient.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/incidentals/{id} = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	updated := decodeIncidental(t, rec)
	if updated.MinimumPerMember == nil || *updated.MinimumPerMember != 30_000 {
		t.Errorf("minimum_per_member = %v, want 30000", updated.MinimumPerMember)
	}

	detailRec := getIncidentalDetail(t, r, envelope.PurposeID)
	var detail incidentalDetailResponse
	if err := json.NewDecoder(detailRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decoding detail response: %v", err)
	}
	if len(detail.Recipients) != 1 || detail.Recipients[0].MemberID != recipient.ID {
		t.Errorf("recipients = %+v, want exactly [%d]", detail.Recipients, recipient.ID)
	}
}

// TestPatchIncidentalParticipationSetsAndClearsTheTarget (#381): the target
// rides the same PATCH as the minimum, and omitting it clears it - the field
// fully replaces its facet like the others.
func TestPatchIncidentalParticipationSetsAndClearsTheTarget(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	target := int64(750_000)
	rec := patchIncidentalParticipation(t, r, envelope.PurposeID, updateIncidentalParticipationRequest{TargetAmount: &target})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH target = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := decodeIncidental(t, rec); got.TargetAmount == nil || *got.TargetAmount != 750_000 {
		t.Errorf("target_amount = %v, want 750000", got.TargetAmount)
	}

	rec = patchIncidentalParticipation(t, r, envelope.PurposeID, updateIncidentalParticipationRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH without target = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := decodeIncidental(t, rec); got.TargetAmount != nil {
		t.Errorf("target_amount after omitting it = %v, want null", got.TargetAmount)
	}

	zero := int64(0)
	rec = patchIncidentalParticipation(t, r, envelope.PurposeID, updateIncidentalParticipationRequest{TargetAmount: &zero})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH target 0 = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestGetIncidentalParticipationReturnsExpectedAndUnexpected is the read
// route's own success path: an expected member reads Belum with nothing
// given, and a later joiner's own gift surfaces under Unexpected.
func TestGetIncidentalParticipationReturnsExpectedAndUnexpected(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	expectedMember := createMemberFor(t, r, "Expected Member")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	laterJoinerJoinedOn := "2026-08-05"
	laterJoinerRec := postMember(t, r, memberRequest{Name: "Later Joiner", JoinedOn: &laterJoinerJoinedOn})
	if laterJoinerRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members(later joiner) = %d, want %d", laterJoinerRec.Code, http.StatusCreated)
	}
	var laterJoiner memberResponse
	if err := json.NewDecoder(laterJoinerRec.Body).Decode(&laterJoiner); err != nil {
		t.Fatalf("decoding later joiner: %v", err)
	}

	if rec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 15_000, OccurredOn: "2026-08-12", MemberID: &laterJoiner.ID,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions(later joiner's gift) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getIncidentalParticipation(t, r, envelope.PurposeID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/incidentals/{id}/participation = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var participation incidentalParticipationResponse
	if err := json.NewDecoder(rec.Body).Decode(&participation); err != nil {
		t.Fatalf("decoding participation response: %v", err)
	}

	var foundExpected bool
	for _, p := range participation.Expected {
		if p.Member.ID == expectedMember.ID {
			foundExpected = true
			if p.State != "belum" {
				t.Errorf("expected member's state = %q, want %q", p.State, "belum")
			}
		}
		if p.Member.ID == laterJoiner.ID {
			t.Error("later joiner found in Expected, want them excluded")
		}
	}
	if !foundExpected {
		t.Fatal("expected member missing from the response's Expected list")
	}

	var foundUnexpected bool
	for _, u := range participation.Unexpected {
		if u.Member.ID == laterJoiner.ID {
			foundUnexpected = true
			if u.ContributedAmount != 15_000 {
				t.Errorf("later joiner's contributed_amount = %d, want 15000", u.ContributedAmount)
			}
		}
	}
	if !foundUnexpected {
		t.Fatal("later joiner's gift missing from the response's Unexpected list")
	}
}

// TestPostDuesPaymentReversalAcceptsANamedContribution: the same reversal
// route ADR-029 built now also accepts a named contribution's transaction
// id (ADR-034) - no new route, no new request shape.
func TestPostDuesPaymentReversalAcceptsANamedContribution(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	member := createMemberFor(t, r, "Jane")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	postRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &member.ID,
	})
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", postRec.Code, http.StatusCreated, postRec.Body.String())
	}
	var contribution transactionResponse
	if err := json.NewDecoder(postRec.Body).Decode(&contribution); err != nil {
		t.Fatalf("decoding contribution response: %v", err)
	}

	rec := postDuesPaymentReversal(t, r, contribution.ID, reverseDuesPaymentRequest{OccurredOn: "2026-08-15"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments/{id}/reversal(contribution) = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var reversal transactionResponse
	if err := json.NewDecoder(rec.Body).Decode(&reversal); err != nil {
		t.Fatalf("decoding reversal response: %v", err)
	}
	if reversal.DuesPeriod != nil {
		t.Errorf("reversal.dues_period = %v, want nil - a contribution never carries one", reversal.DuesPeriod)
	}
	if reversal.MemberID == nil || *reversal.MemberID != member.ID {
		t.Errorf("reversal.member_id = %v, want %d", reversal.MemberID, member.ID)
	}
}

// TestPostPurposeCorrectionRefusesANamedContribution: mapped to its own 409,
// distinct from the dues one, exactly the shape mapLedgerError already uses
// for every other PostPurposeCorrection refusal.
func TestPostPurposeCorrectionRefusesANamedContribution(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	member := createMemberFor(t, r, "Jane")
	envelope := openIncidentalFor(t, r, "Sunatan", "2026-08-01")

	titipanRec := postPassThroughPurpose(t, r, "Titipan")
	if titipanRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/pass-through-purposes = %d, want %d (body: %s)", titipanRec.Code, http.StatusCreated, titipanRec.Body.String())
	}
	var titipan purposeResponse
	if err := json.NewDecoder(titipanRec.Body).Decode(&titipan); err != nil {
		t.Fatalf("decoding purpose response: %v", err)
	}

	postRec := postTransaction(t, r, transactionRequest{
		AccountID: setup.CashAccountID(t), PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 25_000, OccurredOn: "2026-08-12", MemberID: &member.ID,
	})
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", postRec.Code, http.StatusCreated, postRec.Body.String())
	}
	var contribution transactionResponse
	if err := json.NewDecoder(postRec.Body).Decode(&contribution); err != nil {
		t.Fatalf("decoding contribution response: %v", err)
	}

	rec := postPurposeCorrection(t, r, contribution.ID, purposeCorrectionRequest{PurposeID: titipan.ID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/transactions/{id}/purpose-correction(named contribution) = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "purpose_correction_named_contribution" {
		t.Errorf("error code = %q, want %q", got.Code, "purpose_correction_named_contribution")
	}
}
