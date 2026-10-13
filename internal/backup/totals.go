// totals.go is the one place a backup's numbers are compared with its rows
// (ADR-012's "a restore proves its numbers", #496). Every path that makes or
// accepts a backup goes through compareTotals: the download and dumps
// (CheckTotals on the document just read, before a byte is written), the
// restore inspect step (ParseUpload, before the password prompt), and the
// restore (verifyTotals, against the rows just inserted). Integers, compared
// exactly: no tolerance, no float (ADR-015, CLAUDE.md rule 2).

package backup

import (
	"fmt"

	"github.com/kerti/uruni/internal/money"
)

// CheckTotals recomputes doc's fund, account and purpose balances from doc's
// transaction rows and compares them exactly with doc.Totals. A document whose
// totals disagree with its rows, or fail to cover every fund, account and
// purpose it lists, or name one it does not, is refused with ErrTotalsMismatch.
//
// The sum mirrors the three generated balance queries (FundBalance,
// AccountBalance, PurposeBalance: direction 'in' adds, anything else
// subtracts, scoped by fund_id plus account_id or purpose_id) in checked
// money.Amount arithmetic. It is a second implementation on purpose: the
// export reads totals with those queries inside the transaction that read the
// rows, and this check, having only the finished document, proves the two
// agree before the document is trusted anywhere.
func CheckTotals(doc Document) error {
	got, err := totalsFromRows(doc)
	if err != nil {
		return err
	}
	return compareTotals(doc.Totals, got)
}

type scopedID struct{ fundID, id int64 }

// totalsFromRows is the in-memory recompute behind CheckTotals: one total per
// fund, account and purpose doc lists, in doc's row order.
func totalsFromRows(doc Document) (Totals, error) {
	byFund := make(map[int64]money.Amount, len(doc.Funds))
	byAccount := make(map[scopedID]money.Amount, len(doc.Accounts))
	byPurpose := make(map[scopedID]money.Amount, len(doc.Purposes))

	add := func(m map[int64]money.Amount, k int64, t Transaction) error {
		v, err := signed(m[k], t)
		m[k] = v
		return err
	}
	addScoped := func(m map[scopedID]money.Amount, k scopedID, t Transaction) error {
		v, err := signed(m[k], t)
		m[k] = v
		return err
	}

	for _, t := range doc.Transactions {
		if err := add(byFund, t.FundID, t); err != nil {
			return Totals{}, err
		}
		if err := addScoped(byAccount, scopedID{t.FundID, t.AccountID}, t); err != nil {
			return Totals{}, err
		}
		if err := addScoped(byPurpose, scopedID{t.FundID, t.PurposeID}, t); err != nil {
			return Totals{}, err
		}
	}

	out := Totals{
		Funds:    make([]FundTotal, 0, len(doc.Funds)),
		Accounts: make([]AccountTotal, 0, len(doc.Accounts)),
		Purposes: make([]PurposeTotal, 0, len(doc.Purposes)),
	}
	for _, f := range doc.Funds {
		out.Funds = append(out.Funds, FundTotal{FundID: f.ID, Balance: byFund[f.ID].Int64()})
	}
	for _, a := range doc.Accounts {
		out.Accounts = append(out.Accounts, AccountTotal{AccountID: a.ID, Balance: byAccount[scopedID{a.FundID, a.ID}].Int64()})
	}
	for _, p := range doc.Purposes {
		out.Purposes = append(out.Purposes, PurposeTotal{PurposeID: p.ID, Balance: byPurpose[scopedID{p.FundID, p.ID}].Int64()})
	}
	return out, nil
}

// signed folds one transaction into a running balance the way the SQL does:
// 'in' adds, anything else (the schema's CHECK leaves only 'out') subtracts.
func signed(running money.Amount, t Transaction) (money.Amount, error) {
	var (
		v   money.Amount
		err error
	)
	if t.Direction == "in" {
		v, err = running.Add(money.FromDB(t.Amount))
	} else {
		v, err = running.Sub(money.FromDB(t.Amount))
	}
	if err != nil {
		return running, fmt.Errorf("%w: summing transaction %d: %v", ErrMalformedBackup, t.ID, err) //nolint:errorlint // ErrMalformedBackup is the one callers match against
	}
	return v, nil
}

// compareTotals is the one comparison every path shares: claimed (a document's
// totals block) against recomputed (from its rows, or the database a restore
// just filled). Both directions are checked: a recomputed figure the claim
// lacks is as much a mismatch as a differing one, so a block cannot pass by
// omitting the wrong number. Each id may appear once in claimed.
func compareTotals(claimed, recomputed Totals) error {
	if err := compareSet("fund", fundTotalPairs(claimed.Funds), fundTotalPairs(recomputed.Funds)); err != nil {
		return err
	}
	if err := compareSet("account", accountTotalPairs(claimed.Accounts), accountTotalPairs(recomputed.Accounts)); err != nil {
		return err
	}
	return compareSet("purpose", purposeTotalPairs(claimed.Purposes), purposeTotalPairs(recomputed.Purposes))
}

type idBalance struct{ id, balance int64 }

func fundTotalPairs(in []FundTotal) []idBalance {
	out := make([]idBalance, len(in))
	for i, x := range in {
		out[i] = idBalance{x.FundID, x.Balance}
	}
	return out
}

func accountTotalPairs(in []AccountTotal) []idBalance {
	out := make([]idBalance, len(in))
	for i, x := range in {
		out[i] = idBalance{x.AccountID, x.Balance}
	}
	return out
}

func purposeTotalPairs(in []PurposeTotal) []idBalance {
	out := make([]idBalance, len(in))
	for i, x := range in {
		out[i] = idBalance{x.PurposeID, x.Balance}
	}
	return out
}

func compareSet(what string, claimed, recomputed []idBalance) error {
	claimedBy := make(map[int64]int64, len(claimed))
	for _, c := range claimed {
		if _, dup := claimedBy[c.id]; dup {
			return fmt.Errorf("%w: %s %d appears twice in the file's totals", ErrTotalsMismatch, what, c.id)
		}
		claimedBy[c.id] = c.balance
	}
	for _, r := range recomputed {
		want, ok := claimedBy[r.id]
		if !ok {
			return fmt.Errorf("%w: %s %d recomputed %d, file has no total for it", ErrTotalsMismatch, what, r.id, r.balance)
		}
		if want != r.balance {
			return fmt.Errorf("%w: %s %d recomputed %d, file says %d", ErrTotalsMismatch, what, r.id, r.balance, want)
		}
		delete(claimedBy, r.id)
	}
	for id := range claimedBy {
		return fmt.Errorf("%w: file has a total for %s %d, which it does not list", ErrTotalsMismatch, what, id)
	}
	return nil
}
