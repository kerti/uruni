package report

import "github.com/kerti/uruni/internal/ledger"

// This file is the Go port of web/src/components/TransactionRowLabel.tsx's
// rowLabelFor and web/src/copy/id.ts's rowLabels (#257): a row's label is built
// at display time from facts, never a stored note, and a neighbour reads the
// same words the treasurer does in Riwayat. ADR-035 calls this the third
// hand-kept copy; TestReportRowLabelsMatchTheSPA pins a handful of strings
// against the SPA.
//
// Two differences from Riwayat, as the report hides what only the treasurer
// needs (ADR-035):
//   - "Saldo awal" and "Penyesuaian" carry no " - <location>" suffix: the
//     report never names where the money sits.
//   - A purpose move is one "Dipindah: <from> -> <to>" row whatever its
//     reason, where Riwayat words a roll, a correction and a Pindah pos apart.
//
// Every adjustment that is not a reversal (a cek kas fix or one posted alone)
// reads "Penyesuaian" in both, so a row and the walk line it sums into share a
// name (ADR-038).

// entryLabel is the label line for an ordinary row, or "" for a plain row
// (shown with only its purpose and amount).
func (c Copy) entryLabel(e ledger.ReportEntry) string {
	member := ""
	if e.MemberName != nil {
		member = *e.MemberName
	}
	period := ""
	switch {
	case len(e.DuesPeriods) > 0:
		// One entry for all of a payment's months (ADR-048); a single month reads
		// as it always did.
		period = c.periodsText(e.DuesPeriods)
	case e.DuesPeriod != nil:
		period = c.monthName(*e.DuesPeriod)
	}

	switch e.Kind {
	case ledger.TransactionKindDues:
		return c.RowDues(period, member)
	case ledger.TransactionKindOpening:
		return c.RowOpening
	case ledger.TransactionKindReimbursement:
		return member
	case ledger.TransactionKindAdjustment:
		// A reversal first: a dues reversal carries a period, a contribution reversal
		// names a member and has no period, and the reversal of an unnamed row names
		// nobody at all (ADR-042). Every other adjustment is "Penyesuaian", its line
		// in the month's walk (ADR-038); the report says no more, never naming the
		// location.
		if e.IsReversal {
			if e.DuesPeriod != nil {
				return c.RowDuesReversal(period, member)
			}
			if e.MemberName == nil {
				date := ""
				if e.ReversesOccurredOn != nil {
					date = c.longDate(*e.ReversesOccurredOn)
				}
				return c.RowReversal(date)
			}
			return c.RowContributionReversal(member)
		}
		return c.RowAdjustment
	case ledger.TransactionKindNormal:
		// A named contribution says who gave; any other normal row is plain.
		if e.MemberName != nil {
			return member
		}
		return ""
	}
	return ""
}

// entryKind names an ordinary row's kind: icon is the page's kind icon (""
// for a row that has none, as in Riwayat), word the PDF's Jenis column, and
// spoken whether the page needs word read aloud beside the icon - not when the
// label already starts with it (Saldo awal, Penyesuaian, Pembatalan). The
// icons are Riwayat's (rowLabel.ts).
func (c Copy) entryKind(e ledger.ReportEntry) (icon, word string, spoken bool) {
	switch e.Kind {
	case ledger.TransactionKindDues:
		return "dues", c.KindDues, true
	case ledger.TransactionKindOpening:
		return "opening", c.RowOpening, false
	case ledger.TransactionKindReimbursement:
		return "settlement", c.KindSettlement, true
	case ledger.TransactionKindAdjustment:
		if e.IsReversal {
			return "reversal", c.KindReversal, false
		}
		return "adjustment", c.RowAdjustment, false
	case ledger.TransactionKindNormal:
		if e.MemberName != nil {
			return "contribution", c.KindContribution, true
		}
	}
	if e.Direction == ledger.ReportDirectionOut {
		return "", c.KindOut, false
	}
	return "", c.KindIn, false
}

// moveLabel is "Dipindah: <from> -> <to>", with a real arrow.
func (c Copy) moveLabel(m ledger.ReportMove) string {
	return c.RowMoved(m.FromPurposeName, m.ToPurposeName)
}
