package backup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// upgrade.go holds the steps that bring an older uruni.json up to the current
// FormatVersion (ADR-012). They run in ParseUpload, so the in-app restore, the
// inspect step and `uruni rebuild` all take an older file and nothing downstream
// ever sees one.

// minUpgradableFormat is the oldest format_version the importer has a step for.
const minUpgradableFormat int64 = 1

// CanUpgradeFormat reports whether a file of this format_version can be read:
// it is the current version, or an older one with an upgrade step. The stored-
// backup list offers its restore button on this, not on equality with
// FormatVersion, because the one place judging a version is ParseUpload.
func CanUpgradeFormat(version int64) bool {
	return version >= minUpgradableFormat && version <= FormatVersion
}

// decodeDocument decodes jsonBytes strictly (an unknown field is refused) in the
// shape its version had, and upgrades it to the current Document.
func decodeDocument(version int64, jsonBytes []byte) (Document, error) {
	if version == FormatVersion {
		var doc Document
		if err := decodeStrict(jsonBytes, &doc); err != nil {
			return Document{}, err
		}
		return doc, nil
	}

	// Only version 1 is older than the current one today. A future bump adds its
	// step in front of this one and chains: v1 -> v2 -> v3.
	var v1 documentV1
	if err := decodeStrict(jsonBytes, &v1); err != nil {
		return Document{}, err
	}
	return upgradeV1(v1)
}

func decodeStrict(jsonBytes []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: decoding %s: %v", ErrMalformedBackup, jsonFilename, err) //nolint:errorlint
	}
	return nil
}

// documentV1 is format_version 1's shape: Document before ADR-040 and ADR-041.
// Only fundV1, rateV1 and memberV1 differ, and there is no member_tiers array.
type documentV1 struct {
	FormatVersion int64 `json:"format_version"`

	Users     []User     `json:"users"`
	Funds     []fundV1   `json:"funds"`
	Accounts  []Account  `json:"accounts"`
	Purposes  []Purpose  `json:"purposes"`
	DuesTiers []DuesTier `json:"dues_tiers"`
	DuesRates []rateV1   `json:"dues_rates"`
	Members   []memberV1 `json:"members"`

	Transfers      []Transfer      `json:"transfers"`
	Reimbursements []Reimbursement `json:"reimbursements"`
	Transactions   []Transaction   `json:"transactions"`
	Receipts       []Receipt       `json:"receipts"`

	Reconciliations      []Reconciliation      `json:"reconciliations"`
	ReconciliationLines  []ReconciliationLine  `json:"reconciliation_lines"`
	Incidentals          []Incidental          `json:"incidentals"`
	IncidentalRecipients []IncidentalRecipient `json:"incidental_recipients"`

	Totals Totals `json:"totals"`
}

// fundV1 is a v1 fund: no time_zone (ADR-041), so a v1 file carrying one is
// refused as an unknown field.
type fundV1 struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	ReportSlug string `json:"report_slug"`
	CreatedAt  int64  `json:"created_at"`
}

// rateV1 is a v1 rate: no correction number and no deletion mark (ADR-040).
type rateV1 struct {
	ID            int64  `json:"id"`
	TierID        int64  `json:"tier_id"`
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
	CreatedAt     int64  `json:"created_at"`
}

// memberV1 is a v1 member: the tier lived on the row, and JoinedOn stayed a
// pointer because a file from before #471 can carry null.
type memberV1 struct {
	ID         int64   `json:"id"`
	FundID     int64   `json:"fund_id"`
	Name       string  `json:"name"`
	TierID     *int64  `json:"tier_id"`
	JoinedOn   *string `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

// upgradeV1 is the v1 -> v2 step (ADR-040, ADR-041). The totals block is
// unchanged: no ledger row moves.
//
//   - Every fund takes time_zone tz.Default: every v1 file was written when the
//     whole app reckoned in WIB, so nothing it says moves.
//   - A null joined_on takes the fund's adoption date (see adoptionDates), what
//     the column meant before it became NOT NULL, so no member's dues move.
//   - One member_tier row per tiered member: correction 0, from the month of
//     joined_on, created_at from the member's, ids 1.. in member-id order (the
//     file's array order is not trusted; the ids are minted here, so their order
//     is made deterministic). An untiered member gets no row.
//   - A v1 rate becomes correction 0, deleted false.
func upgradeV1(v1 documentV1) (Document, error) {
	doc := Document{
		FormatVersion:        FormatVersion,
		Users:                v1.Users,
		Funds:                make([]Fund, 0, len(v1.Funds)),
		Accounts:             v1.Accounts,
		Purposes:             v1.Purposes,
		DuesTiers:            v1.DuesTiers,
		DuesRates:            make([]DuesRate, 0, len(v1.DuesRates)),
		Members:              make([]Member, 0, len(v1.Members)),
		MemberTiers:          []MemberTier{},
		Transfers:            v1.Transfers,
		Reimbursements:       v1.Reimbursements,
		Transactions:         v1.Transactions,
		Receipts:             v1.Receipts,
		Reconciliations:      v1.Reconciliations,
		ReconciliationLines:  v1.ReconciliationLines,
		Incidentals:          v1.Incidentals,
		IncidentalRecipients: v1.IncidentalRecipients,
		Totals:               v1.Totals,
	}

	for _, f := range v1.Funds {
		doc.Funds = append(doc.Funds, Fund{
			ID: f.ID, Name: f.Name, Currency: f.Currency, TimeZone: tz.Default, ReportSlug: f.ReportSlug, CreatedAt: f.CreatedAt,
		})
	}
	for _, r := range v1.DuesRates {
		doc.DuesRates = append(doc.DuesRates, DuesRate{
			ID: r.ID, TierID: r.TierID, Amount: r.Amount, EffectiveFrom: r.EffectiveFrom, CreatedAt: r.CreatedAt,
		})
	}

	adopted, err := adoptionDates(doc)
	if err != nil {
		return Document{}, err
	}
	for _, m := range v1.Members {
		joinedOn, ok := adopted[m.FundID]
		if m.JoinedOn != nil {
			joinedOn, ok = *m.JoinedOn, true
		}
		if !ok {
			return Document{}, fmt.Errorf("%w: member %d belongs to fund %d, which the file does not carry", ErrMalformedBackup, m.ID, m.FundID)
		}
		if t, err := time.Parse(time.DateOnly, joinedOn); err != nil || t.Format(time.DateOnly) != joinedOn {
			return Document{}, fmt.Errorf("%w: member %d's joined_on %q is not a calendar date", ErrMalformedBackup, m.ID, joinedOn)
		}
		// member_tier's first row starts at the join month, which has a 2000-01 floor,
		// so an earlier date cannot be backfilled (and member.joined_on has the same
		// floor, so such a backup is unrestorable anyway).
		if joinedOn < "2000-01-01" {
			return Document{}, fmt.Errorf("%w: member %d's joined_on %q is before 2000-01-01", ErrMalformedBackup, m.ID, joinedOn)
		}
		doc.Members = append(doc.Members, Member{
			ID: m.ID, FundID: m.FundID, Name: m.Name, JoinedOn: joinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
		})
	}

	// doc.Members is v1.Members in the same order, so index i is the same member.
	type tieredMember struct {
		member Member
		tierID *int64
	}
	var tiered []tieredMember
	for i, m := range v1.Members {
		if m.TierID != nil {
			tiered = append(tiered, tieredMember{member: doc.Members[i], tierID: m.TierID})
		}
	}
	slices.SortFunc(tiered, func(a, b tieredMember) int {
		switch {
		case a.member.ID < b.member.ID:
			return -1
		case a.member.ID > b.member.ID:
			return 1
		}
		return 0
	})
	for i, t := range tiered {
		doc.MemberTiers = append(doc.MemberTiers, MemberTier{
			ID: int64(i + 1), FundID: t.member.FundID, MemberID: t.member.ID, TierID: t.tierID,
			EffectiveFrom: t.member.JoinedOn[:len("2006-01")], Correction: 0, CreatedAt: t.member.CreatedAt,
		})
	}
	return doc, nil
}

// adoptionDates maps each fund in doc to the day its Uruni history starts
// (PRD 7.1): the earliest opening-balance row's occurred_on, or, for a fund
// with none, the day the fund was created in the fund's own zone (ADR-041). It is what a null
// member.joined_on meant before #471 made the column NOT NULL, so upgrading an
// older backup this way changes no member's dues.
func adoptionDates(doc Document) (map[int64]string, error) {
	out := make(map[int64]string, len(doc.Funds))
	for _, f := range doc.Funds {
		loc, err := tz.Load(f.TimeZone)
		if err != nil {
			return nil, fmt.Errorf("%w: fund %d's time_zone: %v", ErrMalformedBackup, f.ID, err) //nolint:errorlint
		}
		out[f.ID] = time.Unix(f.CreatedAt, 0).In(loc).Format("2006-01-02")
	}
	opened := make(map[int64]string, len(doc.Funds))
	for _, t := range doc.Transactions {
		if t.Kind != "opening" {
			continue
		}
		if d, ok := opened[t.FundID]; !ok || t.OccurredOn < d {
			opened[t.FundID] = t.OccurredOn
		}
	}
	for id, d := range opened {
		out[id] = d
	}
	return out, nil
}
