package http

import "github.com/kerti/uruni/internal/ledger"

// This file is the Go port of web/src/components/TransactionRowLabel.tsx's
// rowLabelFor and web/src/copy/id.ts's rowLabels (#257): the label a row
// carries is built at display time from facts, never read from a stored note,
// and the neighbour reads the same words the treasurer does in Riwayat.
// ADR-035 names this the third hand-kept copy; TestReportRowLabelsMatchTheSPA
// pins a handful of strings against what the SPA produces.
//
// Two deliberate differences from Riwayat, both because the report hides what
// only the treasurer needs (ADR-035):
//   - "Saldo awal" and "Penyesuaian" carry no " - <location>" suffix: the
//     report never names where the money sits.
//   - A purpose move is one "Dipindah: <from> -> <to>" row whatever its
//     reason, where Riwayat words a roll, a correction and a Pindah pos apart.

// entryLabel is the label line for an ordinary row, or "" for a plain row
// the treasurer recorded (which shows only its purpose and amount).
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
		// A reversal first: it and a reconciliation fix are the schema's two
		// disjoint shapes of this kind, and a dues reversal carries a period
		// where a contribution reversal never does.
		if e.IsReversal {
			if e.DuesPeriod != nil {
				return c.RowDuesReversal(period, member)
			}
			return c.RowContributionReversal(member)
		}
		if e.IsReconciliationFix {
			return c.RowReconciliationFix
		}
		return ""
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
