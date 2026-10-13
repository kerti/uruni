package http

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
func (c reportCopy) entryLabel(e ledger.ReportEntry) string {
	member := ""
	if e.MemberName != nil {
		member = *e.MemberName
	}
	period := ""
	if e.DuesPeriod != nil {
		period = c.monthName(*e.DuesPeriod)
	}

	switch e.Kind {
	case "dues":
		return c.RowDues(period, member)
	case "opening":
		return c.RowOpening
	case "reimbursement":
		return member
	case "adjustment":
		// A reversal first: a dues reversal carries a period, a contribution reversal
		// never does. Every other adjustment (a cek kas fix, or one posted alone) is
		// "Penyesuaian", its line in the month's walk (ADR-038); the report says no
		// more, never naming the location.
		if e.IsReversal {
			if e.DuesPeriod != nil {
				return c.RowDuesReversal(period, member)
			}
			return c.RowContributionReversal(member)
		}
		return c.RowAdjustment
	case "normal":
		// A named contribution says who gave; any other normal row is plain.
		if e.MemberName != nil {
			return member
		}
		return ""
	}
	return ""
}

// moveLabel is "Dipindah: <from> -> <to>", with a real arrow.
func (c reportCopy) moveLabel(m ledger.ReportMove) string {
	return c.RowMoved(m.FromPurposeName, m.ToPurposeName)
}
