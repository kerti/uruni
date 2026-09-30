package backup

import "github.com/kerti/uruni/internal/store"

// toX below are thin, mechanical maps from store's sqlc-generated row types
// to this package's own exported shape (backup.go's own comment says why
// the shape is not the generated struct directly: json tags need to be
// snake_case and stable, where the generated struct carries neither). Each
// one always returns a non-nil (possibly empty) slice, matching
// BuildDocument's own "always an array" contract.

func toUsers(rows []store.User) []User {
	out := make([]User, 0, len(rows))
	for _, r := range rows {
		out = append(out, User{ID: r.ID, Email: r.Email, PasswordHash: r.PasswordHash, CreatedAt: r.CreatedAt})
	}
	return out
}

func toFunds(rows []store.Fund) []Fund {
	out := make([]Fund, 0, len(rows))
	for _, r := range rows {
		out = append(out, Fund{ID: r.ID, Name: r.Name, Currency: r.Currency, ReportSlug: r.ReportSlug, CreatedAt: r.CreatedAt})
	}
	return out
}

func toAccounts(rows []store.Account) []Account {
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, Account{
			ID: r.ID, FundID: r.FundID, Kind: r.Kind, Name: r.Name,
			CreatedAt: r.CreatedAt, InactiveOn: r.InactiveOn,
		})
	}
	return out
}

func toPurposes(rows []store.Purpose) []Purpose {
	out := make([]Purpose, 0, len(rows))
	for _, r := range rows {
		out = append(out, Purpose{ID: r.ID, FundID: r.FundID, Kind: r.Kind, Name: r.Name, CreatedAt: r.CreatedAt})
	}
	return out
}

func toDuesTiers(rows []store.DuesTier) []DuesTier {
	out := make([]DuesTier, 0, len(rows))
	for _, r := range rows {
		out = append(out, DuesTier{ID: r.ID, FundID: r.FundID, Name: r.Name, CreatedAt: r.CreatedAt})
	}
	return out
}

func toDuesRates(rows []store.DuesRate) []DuesRate {
	out := make([]DuesRate, 0, len(rows))
	for _, r := range rows {
		out = append(out, DuesRate{
			ID: r.ID, TierID: r.TierID, Amount: r.Amount,
			EffectiveFrom: r.EffectiveFrom, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toMembers(rows []store.Member) []Member {
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{
			ID: r.ID, FundID: r.FundID, Name: r.Name, TierID: r.TierID,
			JoinedOn: r.JoinedOn, InactiveOn: r.InactiveOn, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toTransfers(rows []store.Transfer) []Transfer {
	out := make([]Transfer, 0, len(rows))
	for _, r := range rows {
		out = append(out, Transfer{
			ID: r.ID, FundID: r.FundID, Kind: r.Kind,
			CorrectsTransactionID: r.CorrectsTransactionID, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

// toReimbursements reads store.ListReimbursementsByFundRow, the fund-scoped
// listing every other caller of it uses too - its extra Settled column
// (whether a settling transaction exists) is derived, not stored, so it is
// left out here: uruni.json exports the reimbursement table's own columns,
// and a restore recomputes "settled" from the transactions it just
// inserted exactly as this column already does.
func toReimbursements(rows []store.ListReimbursementsByFundRow) []Reimbursement {
	out := make([]Reimbursement, 0, len(rows))
	for _, r := range rows {
		out = append(out, Reimbursement{
			ID: r.ID, FundID: r.FundID, MemberID: r.MemberID, PurposeID: r.PurposeID,
			Amount: r.Amount, IncurredOn: r.IncurredOn, WaivedOn: r.WaivedOn,
			Note: r.Note, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toTransactions(rows []store.Transaction) []Transaction {
	out := make([]Transaction, 0, len(rows))
	for _, r := range rows {
		out = append(out, Transaction{
			ID: r.ID, FundID: r.FundID, AccountID: r.AccountID, PurposeID: r.PurposeID,
			Direction: r.Direction, Amount: r.Amount, OccurredOn: r.OccurredOn, Kind: r.Kind,
			MemberID: r.MemberID, DuesPeriod: r.DuesPeriod, ReimbursementID: r.ReimbursementID,
			TransferID: r.TransferID, ReversesTransactionID: r.ReversesTransactionID,
			Note: r.Note, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toReceipts(rows []store.Receipt) []Receipt {
	out := make([]Receipt, 0, len(rows))
	for _, r := range rows {
		out = append(out, Receipt{
			ID: r.ID, FundID: r.FundID, TransactionID: r.TransactionID,
			ReimbursementID: r.ReimbursementID, Path: r.Path, UploadedAt: r.UploadedAt,
		})
	}
	return out
}

func toReconciliations(rows []store.Reconciliation) []Reconciliation {
	out := make([]Reconciliation, 0, len(rows))
	for _, r := range rows {
		out = append(out, Reconciliation{
			ID: r.ID, FundID: r.FundID, PerformedAt: r.PerformedAt,
			ThroughTransactionID: r.ThroughTransactionID, Note: r.Note, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toReconciliationLines(rows []store.ReconciliationLine) []ReconciliationLine {
	out := make([]ReconciliationLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReconciliationLine{
			ID: r.ID, FundID: r.FundID, ReconciliationID: r.ReconciliationID, AccountID: r.AccountID,
			RecordedAmount: r.RecordedAmount, ActualAmount: r.ActualAmount,
			DifferenceAmount: r.DifferenceAmount, Resolution: r.Resolution,
			AdjustmentTransactionID: r.AdjustmentTransactionID,
		})
	}
	return out
}

func toIncidentals(rows []store.Incidental) []Incidental {
	out := make([]Incidental, 0, len(rows))
	for _, r := range rows {
		out = append(out, Incidental{
			PurposeID: r.PurposeID, Occasion: r.Occasion, TargetAmount: r.TargetAmount,
			OpenedOn: r.OpenedOn, ClosedOn: r.ClosedOn, MinimumPerMember: r.MinimumPerMember,
			CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func toIncidentalRecipients(rows []store.IncidentalRecipient) []IncidentalRecipient {
	out := make([]IncidentalRecipient, 0, len(rows))
	for _, r := range rows {
		out = append(out, IncidentalRecipient{FundID: r.FundID, PurposeID: r.PurposeID, MemberID: r.MemberID})
	}
	return out
}
