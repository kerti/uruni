package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ZipFilename is the download's name (GET /api/backup, ADR-012), dated so a
// second download never lands as "uruni(1).zip". The app names the file with
// the treasurer's local date (web/src/lib/backup.ts); this is the name a
// direct request gets. exportedOn is formatted in whatever zone it carries,
// so the handler passes it in the fund's zone (#379, ADR-041): the server clock
// may be UTC, a day behind in WIB before 07:00 and in WIT before 09:00.
func ZipFilename(exportedOn time.Time) string {
	return "uruni-" + exportedOn.Format("2006-01-02") + ".zip"
}

// jsonFilename and receiptsDir are the zip's two entries (ADR-012): the
// canonical export and the folder of receipt images it references by name.
const (
	jsonFilename = "uruni.json"
	receiptsDir  = "receipts/"
)

// Export writes the whole-instance backup zip to w: uruni.json plus every
// receipt image, read from uploadsDir under its stored name (ADR-011). It
// changes nothing on disk but what w is.
//
// The zip is streamed, never built in memory (#533): each receipt is copied
// from its file into the archive through one small buffer, so memory stays at
// uruni.json's bytes (capped at MaxDocumentBytes) however many years of photos
// the uploads volume holds. Nothing is written to w until the document has
// been read and checked, so a refusal leaves w empty; a failure while copying
// a receipt can leave it holding a partial zip, which the caller discards.
//
// A receipt whose image file is gone never blocks the backup (maintainer,
// 2026-09-30): its row still goes into uruni.json, the image is skipped, and
// its stored name comes back in missing for the caller to log. The backup is
// as complete as the live instance, never less. Any other read failure is a
// real fault and fails.
func Export(ctx context.Context, sqlDB *sql.DB, uploadsDir string, w io.Writer) (missing []string, err error) {
	plan, err := planDump(ctx, readDocumentFrom(sqlDB), uploadsDir)
	if err != nil {
		return nil, err
	}
	return assembleZip(w, plan.docBytes, plan.receipts, uploadsDir)
}

// BuildZip marshals doc to uruni.json and assembles the finished zip in
// memory, for a caller that wants the archive as bytes - tests building a
// fixture. The producers never call it: they marshal once (marshalDocument),
// hash those bytes for the change-only check (planDump), and only then stream
// the zip (assembleZip) from the same bytes, so an unchanged fund never builds
// one (#501) and a changed one is never held in memory (#533).
func BuildZip(doc Document, receipts []store.Receipt, uploadsDir string) (zipBytes []byte, docBytes []byte, missing []string, err error) {
	docBytes, err = marshalDocument(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	var buf bytes.Buffer
	missing, err = assembleZip(&buf, docBytes, receipts, uploadsDir)
	if err != nil {
		return nil, nil, nil, err
	}
	return buf.Bytes(), docBytes, missing, nil
}

// marshalDocument is the one place uruni.json's bytes are produced. The
// change hash (planDump) and the zip (assembleZip) take these exact bytes.
func marshalDocument(doc Document) ([]byte, error) {
	docBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshaling uruni.json: %w", err)
	}
	if int64(len(docBytes)) > MaxDocumentBytes {
		return nil, fmt.Errorf("%w: %d bytes, more than the %d a restore reads", ErrDocumentTooLarge, len(docBytes), MaxDocumentBytes)
	}
	return docBytes, nil
}

// zipAssembler is assembleZip's type: the seam a test swaps for a counting
// wrapper to prove an unchanged fund assembles no zip.
type zipAssembler func(w io.Writer, docBytes []byte, receipts []store.Receipt, uploadsDir string) (missing []string, err error)

// assembleZip is the expensive half of every producer: it streams the archive
// to w, copying every receipt file in. Nothing that only needs to know whether
// the fund changed may call it (planDump decides from the document bytes and a
// stat).
func assembleZip(w io.Writer, docBytes []byte, receipts []store.Receipt, uploadsDir string) (missing []string, err error) {
	zw := zip.NewWriter(w)

	jsonWriter, err := zw.Create(jsonFilename)
	if err != nil {
		return nil, fmt.Errorf("backup: creating %s in zip: %w", jsonFilename, err)
	}
	if _, err := jsonWriter.Write(docBytes); err != nil {
		return nil, fmt.Errorf("backup: writing %s: %w", jsonFilename, err)
	}

	for _, r := range receipts {
		found, err := addReceiptToZip(zw, uploadsDir, r.Path)
		if err != nil {
			return nil, err
		}
		if !found {
			missing = append(missing, r.Path)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("backup: closing zip: %w", err)
	}
	return missing, nil
}

// addReceiptToZip copies one receipt image from the uploads volume into the
// zip's receipts/ folder under its stored name, the name uruni.json's receipt
// row carries in its path field, so an importer joins them without renaming.
// The file is copied, never read whole.
func addReceiptToZip(zw *zip.Writer, uploadsDir, path string) (found bool, err error) {
	//nolint:gosec // path is receipt.path, always a crypto/rand filename
	// this server generated itself on upload (ADR-011's randomReceiptFilename)
	// and never derived from request input - the same trust boundary
	// internal/http/receipts.go's own reads of this column already rely on.
	f, err := os.Open(filepath.Join(uploadsDir, path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("backup: reading receipt %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	w, err := zw.Create(receiptsDir + path)
	if err != nil {
		return false, fmt.Errorf("backup: creating receipts/%s in zip: %w", path, err)
	}
	if _, err := io.Copy(w, f); err != nil {
		return false, fmt.Errorf("backup: writing receipts/%s: %w", path, err)
	}
	return true, nil
}

// BuildDocument reads every table but session and idempotency_key (ADR-012) and the totals
// block, in the order of Document's comment. It is split out of Export so a
// test can assert on the Document's shape without decoding a zip.
//
// The whole document, every row and total, is read in ONE read transaction
// (#496). Under ADR-004's single connection one read is consistent, but
// about fifteen are not: a write can land between the rows and the totals,
// and the resulting zip passes inspection yet disagrees with itself.
// BeginTx checks the pool's one connection out for the reads, so no
// in-process write can interleave, and SQLite's snapshot isolation inside a
// BEGIN (WAL) covers another process. Totals are read through the same
// transaction, not *ledger.Ledger, whose methods are bound to sqlDB and
// would ask the pool for a second connection while this one is held: a
// deadlock, the same trap Restore documents.
//
// It also returns the receipt rows it read: Export needs their Path for the
// zip's receipts/ folder, and the golden fixture test needs them to seed the
// uploads directory.
func BuildDocument(ctx context.Context, sqlDB *sql.DB) (Document, []store.Receipt, error) {
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Document{}, nil, fmt.Errorf("backup: beginning the read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a read transaction has nothing to commit

	return readDocument(ctx, store.New(tx))
}

// documentReader is how the dump and download paths obtain a Document: a seam
// so a test can hand them a self-contradicting document and watch every
// producer refuse it. Production passes readDocumentFrom(sqlDB).
type documentReader func(ctx context.Context) (Document, []store.Receipt, error)

func readDocumentFrom(sqlDB *sql.DB) documentReader {
	return func(ctx context.Context) (Document, []store.Receipt, error) {
		return BuildDocument(ctx, sqlDB)
	}
}

// readDocument is BuildDocument's body, over a Querier that must already be
// pinned to one transaction. It never opens one itself.
func readDocument(ctx context.Context, q store.Querier) (Document, []store.Receipt, error) {
	users, err := q.ListUsers(ctx)
	if err != nil {
		return Document{}, nil, fmt.Errorf("backup: listing users: %w", err)
	}

	funds, err := q.ListFunds(ctx)
	if err != nil {
		return Document{}, nil, fmt.Errorf("backup: listing funds: %w", err)
	}

	// Every slice starts non-nil: an instance with no funds (or a fund with no
	// reimbursements) exports "[]", never "null", the "always an array" contract
	// of getBalances (internal/http/balances.go).
	doc := Document{
		FormatVersion:        FormatVersion,
		Users:                toUsers(users),
		Funds:                toFunds(funds),
		Accounts:             []Account{},
		Purposes:             []Purpose{},
		DuesTiers:            []DuesTier{},
		DuesRates:            []DuesRate{},
		Members:              []Member{},
		MemberTiers:          []MemberTier{},
		Transfers:            []Transfer{},
		Reimbursements:       []Reimbursement{},
		Transactions:         []Transaction{},
		Receipts:             []Receipt{},
		Reconciliations:      []Reconciliation{},
		ReconciliationLines:  []ReconciliationLine{},
		Incidentals:          []Incidental{},
		IncidentalRecipients: []IncidentalRecipient{},
		Totals: Totals{
			Funds:    []FundTotal{},
			Accounts: []AccountTotal{},
			Purposes: []PurposeTotal{},
		},
	}

	allReceipts := []store.Receipt{}

	for _, fund := range funds {
		accounts, err := q.ListAccountsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing accounts for fund %d: %w", fund.ID, err)
		}
		byID(accounts, func(r store.Account) int64 { return r.ID })
		doc.Accounts = append(doc.Accounts, toAccounts(accounts)...)

		purposes, err := q.ListPurposesByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing purposes for fund %d: %w", fund.ID, err)
		}
		byID(purposes, func(r store.Purpose) int64 { return r.ID })
		doc.Purposes = append(doc.Purposes, toPurposes(purposes)...)

		tiers, err := q.ListDuesTiersByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing dues tiers for fund %d: %w", fund.ID, err)
		}
		byID(tiers, func(r store.DuesTier) int64 { return r.ID })
		doc.DuesTiers = append(doc.DuesTiers, toDuesTiers(tiers)...)

		rates, err := q.ListDuesRatesByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing dues rates for fund %d: %w", fund.ID, err)
		}
		doc.DuesRates = append(doc.DuesRates, toDuesRates(rates)...)

		members, err := q.ListMembersByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing members for fund %d: %w", fund.ID, err)
		}
		byID(members, func(r store.Member) int64 { return r.ID })
		doc.Members = append(doc.Members, toMembers(members)...)

		// Every row, superseded ones included, not the standing view (ADR-040).
		memberTiers, err := q.ListMemberTiersForExport(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing member tiers for fund %d: %w", fund.ID, err)
		}
		doc.MemberTiers = append(doc.MemberTiers, toMemberTiers(memberTiers)...)

		transfers, err := q.ListTransfersByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing transfers for fund %d: %w", fund.ID, err)
		}
		doc.Transfers = append(doc.Transfers, toTransfers(transfers)...)

		reimbursements, err := q.ListReimbursementsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing reimbursements for fund %d: %w", fund.ID, err)
		}
		doc.Reimbursements = append(doc.Reimbursements, toReimbursements(reimbursements)...)

		transactions, err := q.ListTransactionsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing transactions for fund %d: %w", fund.ID, err)
		}
		doc.Transactions = append(doc.Transactions, toTransactions(transactions)...)

		receipts, err := q.ListReceiptsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing receipts for fund %d: %w", fund.ID, err)
		}
		doc.Receipts = append(doc.Receipts, toReceipts(receipts)...)
		allReceipts = append(allReceipts, receipts...)

		reconciliations, err := q.ListReconciliationsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing reconciliations for fund %d: %w", fund.ID, err)
		}
		doc.Reconciliations = append(doc.Reconciliations, toReconciliations(reconciliations)...)

		lines, err := q.ListReconciliationLinesByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing reconciliation lines for fund %d: %w", fund.ID, err)
		}
		doc.ReconciliationLines = append(doc.ReconciliationLines, toReconciliationLines(lines)...)

		incidentals, err := q.ListIncidentalsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing incidentals for fund %d: %w", fund.ID, err)
		}
		doc.Incidentals = append(doc.Incidentals, toIncidentals(incidentals)...)

		recipients, err := q.ListIncidentalRecipientsByFund(ctx, fund.ID)
		if err != nil {
			return Document{}, nil, fmt.Errorf("backup: listing incidental recipients for fund %d: %w", fund.ID, err)
		}
		doc.IncidentalRecipients = append(doc.IncidentalRecipients, toIncidentalRecipients(recipients)...)

		totals, err := readFundTotals(ctx, q, fund.ID, accounts, purposes)
		if err != nil {
			return Document{}, nil, err
		}
		doc.Totals.Funds = append(doc.Totals.Funds, totals.fund)
		doc.Totals.Accounts = append(doc.Totals.Accounts, totals.accounts...)
		doc.Totals.Purposes = append(doc.Totals.Purposes, totals.purposes...)
	}

	return doc, allReceipts, nil
}

type oneFundTotals struct {
	fund     FundTotal
	accounts []AccountTotal
	purposes []PurposeTotal
}

// readFundTotals is the totals block's read (ADR-012's "a restore proves its
// numbers"): every figure comes from the generated FundBalance /
// AccountBalance / PurposeBalance queries, the three GET /api/balances
// composes through internal/ledger and Restore's verifyTotals recomputes
// against. It re-derives no balance itself; q is the export's one read
// transaction, so these figures and the rows beside them are one instant.
func readFundTotals(ctx context.Context, q store.Querier, fundID int64, accounts []store.Account, purposes []store.Purpose) (oneFundTotals, error) {
	fundBalance, err := q.FundBalance(ctx, fundID)
	if err != nil {
		return oneFundTotals{}, fmt.Errorf("backup: fund balance for fund %d: %w", fundID, err)
	}

	out := oneFundTotals{fund: FundTotal{FundID: fundID, Balance: money.FromDB(fundBalance).Int64()}}

	for _, a := range accounts {
		balance, err := q.AccountBalance(ctx, store.AccountBalanceParams{FundID: fundID, AccountID: a.ID})
		if err != nil {
			return oneFundTotals{}, fmt.Errorf("backup: account balance for account %d: %w", a.ID, err)
		}
		out.accounts = append(out.accounts, AccountTotal{AccountID: a.ID, Balance: money.FromDB(balance).Int64()})
	}

	for _, p := range purposes {
		balance, err := q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: fundID, PurposeID: p.ID})
		if err != nil {
			return oneFundTotals{}, fmt.Errorf("backup: purpose balance for purpose %d: %w", p.ID, err)
		}
		out.purposes = append(out.purposes, PurposeTotal{PurposeID: p.ID, Balance: money.FromDB(balance).Int64()})
	}

	return out, nil
}

// byID puts rows the store lists in display order back into id order, the
// canonical document's byte order (TestGoldenFixtureMatchesExport). A backup
// needs an order that never changes, and ids give one.
func byID[T any](rows []T, id func(T) int64) {
	slices.SortFunc(rows, func(a, b T) int {
		switch x, y := id(a), id(b); {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
}
