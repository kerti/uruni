package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ZipFilename is the download's own name (GET /api/backup, ADR-012), dated
// so a second download never lands as "uruni(1).zip" beside the first. The
// app's own download names the file with the treasurer's local date instead
// (web/src/lib/backup.ts); this is the name a direct request gets. exportedOn
// is formatted in whatever zone it carries, so the caller converts it first:
// the handler passes the instant in tz.Jakarta (#379), since the server's
// clock may be UTC, a day behind in WIB before 07:00.
func ZipFilename(exportedOn time.Time) string {
	return "uruni-" + exportedOn.Format("2006-01-02") + ".zip"
}

// jsonFilename and receiptsDir are the zip's two entries (ADR-012): the
// canonical export and the folder of receipt images it references by
// filename rather than embedding.
const (
	jsonFilename = "uruni.json"
	receiptsDir  = "receipts/"
)

// Export builds the whole-instance backup zip: uruni.json plus every
// receipt image, read from uploadsDir under its own stored name (ADR-011).
// It touches nothing - no row is written, no file is deleted - and returns
// the finished zip's bytes for the caller to stream or, in a test, compare
// byte-for-byte against a golden fixture.
//
// The whole zip is built in memory rather than streamed straight to the
// response: a community fund's backup is a few megabytes of JSON and
// downscaled JPEGs (ADR-011 caps every stored receipt at ~1600px, roughly
// quality 80), never the tens or hundreds of megabytes that would make
// buffering a real cost.
//
// A receipt whose image file is gone from the uploads volume never blocks
// the backup (maintainer, 2026-09-30): its row still goes into uruni.json,
// the image is skipped, and its stored name comes back in missing for the
// caller to log. The backup is then exactly as complete as the live
// instance, never less - and a treasurer who lost one photo can still take
// a backup at all. Any other read failure is a real fault and still fails.
func Export(ctx context.Context, sqlDB *sql.DB, uploadsDir string) (zipBytes []byte, missing []string, err error) {
	m, err := buildDumpMaterials(ctx, readDocumentFrom(sqlDB), assembleZip, uploadsDir)
	if err != nil {
		return nil, nil, err
	}
	return m.zipBytes, m.missing, nil
}

// BuildZip marshals doc to uruni.json and assembles the finished zip -
// split out of Export so dumps.go's WriteDump can reach the same bytes
// Export would produce, for the scheduled and boot-time dumps, without a
// second export path (ADR-012's "the logic lives in one package so that
// stays cheap" - this is the one place that turns a Document into zip
// bytes; Export and WriteDump both call it, neither reimplements it).
//
// The producers no longer call BuildZip: they marshal once (marshalDocument),
// hash those bytes for the change-only check (dumps.go's planDump), and only
// then assemble the zip (assembleZip) from the same bytes - so an unchanged
// fund never builds one (#501). BuildZip is the two steps together, for a
// caller that wants the archive unconditionally.
func BuildZip(doc Document, receipts []store.Receipt, uploadsDir string) (zipBytes []byte, docBytes []byte, missing []string, err error) {
	docBytes, err = marshalDocument(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	zipBytes, missing, err = assembleZip(docBytes, receipts, uploadsDir)
	if err != nil {
		return nil, nil, nil, err
	}
	return zipBytes, docBytes, missing, nil
}

// marshalDocument is the one place uruni.json's bytes are produced. The
// change hash (planDump) and the zip (assembleZip) both take these exact
// bytes, so the hash always describes what the zip holds.
func marshalDocument(doc Document) ([]byte, error) {
	docBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: marshaling uruni.json: %w", err)
	}
	return docBytes, nil
}

// zipAssembler is assembleZip's type: the seam a test swaps for a counting
// wrapper to prove that an unchanged fund assembles no zip at all.
type zipAssembler func(docBytes []byte, receipts []store.Receipt, uploadsDir string) (zipBytes []byte, missing []string, err error)

// assembleZip is the expensive half of BuildZip: it reads every receipt file
// into the archive. Nothing that only needs to know whether the fund changed
// may call it (planDump decides that from the document bytes and a stat of
// each receipt, never from this).
func assembleZip(docBytes []byte, receipts []store.Receipt, uploadsDir string) (zipBytes []byte, missing []string, err error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	jsonWriter, err := zw.Create(jsonFilename)
	if err != nil {
		return nil, nil, fmt.Errorf("backup: creating %s in zip: %w", jsonFilename, err)
	}
	if _, err := jsonWriter.Write(docBytes); err != nil {
		return nil, nil, fmt.Errorf("backup: writing %s: %w", jsonFilename, err)
	}

	for _, r := range receipts {
		found, err := addReceiptToZip(zw, uploadsDir, r.Path)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			missing = append(missing, r.Path)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, nil, fmt.Errorf("backup: closing zip: %w", err)
	}
	return buf.Bytes(), missing, nil
}

// addReceiptToZip copies one receipt image from the uploads volume into the
// zip's receipts/ folder under its own stored name - the same name
// uruni.json's matching receipt row names in its own path field, so an
// importer can join the two without any renaming.
func addReceiptToZip(zw *zip.Writer, uploadsDir, path string) (found bool, err error) {
	//nolint:gosec // path is receipt.path, always a crypto/rand filename
	// this server generated itself on upload (ADR-011's randomReceiptFilename)
	// and never derived from request input - the same trust boundary
	// internal/http/receipts.go's own reads of this column already rely on.
	data, err := os.ReadFile(filepath.Join(uploadsDir, path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("backup: reading receipt %s: %w", path, err)
	}
	w, err := zw.Create(receiptsDir + path)
	if err != nil {
		return false, fmt.Errorf("backup: creating receipts/%s in zip: %w", path, err)
	}
	if _, err := w.Write(data); err != nil {
		return false, fmt.Errorf("backup: writing receipts/%s: %w", path, err)
	}
	return true, nil
}

// BuildDocument reads every table but session (ADR-012) and the totals
// block, in the order backup.go's own Document comment names. It is split
// out of Export so a test can assert on the Document's shape directly,
// without also decoding a zip - and so #325's importer can call it (or its
// eventual write-side twin) without going through archive/zip at all.
//
// The whole document - every row and every total - is read inside ONE read
// transaction (#496). Under ADR-004's single connection a bare read is
// consistent, but a sequence of about fifteen of them is not: a write can
// land between the rows and the totals, and the zip that results passes
// inspection yet disagrees with itself. BeginTx checks the pool's one
// connection out for the length of the reads, so no in-process write can
// interleave at all, and SQLite's own snapshot isolation inside a BEGIN
// (WAL) covers a writer in another process. The totals are read through the
// same transaction rather than through *ledger.Ledger, whose methods are
// bound to sqlDB and would ask the pool for a second connection while this
// one is held - a deadlock, the same trap Restore documents.
//
// It also returns the receipt rows it read, since Export needs their Path
// to build the zip's receipts/ folder and BuildDocument's own caller (the
// golden fixture test) needs them to seed the uploads directory it reads
// from.
func BuildDocument(ctx context.Context, sqlDB *sql.DB) (Document, []store.Receipt, error) {
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Document{}, nil, fmt.Errorf("backup: beginning the read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a read transaction has nothing to commit

	return readDocument(ctx, store.New(tx))
}

// documentReader is how the dump and download paths obtain a Document: a
// seam so a test can hand them a document that disagrees with itself and
// watch every producer refuse it. Production always passes
// readDocumentFrom(sqlDB).
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

	// Every slice starts non-nil: an instance with no funds yet (or a fund
	// with, say, no reimbursements) exports "[]", never "null" - the same
	// "always an array" contract getBalances already keeps for the wire
	// shape (internal/http/balances.go).
	doc := Document{
		FormatVersion:        FormatVersion,
		Users:                toUsers(users),
		Funds:                toFunds(funds),
		Accounts:             []Account{},
		Purposes:             []Purpose{},
		DuesTiers:            []DuesTier{},
		DuesRates:            []DuesRate{},
		Members:              []Member{},
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

// readFundTotals is the totals block's own read (ADR-012's "a restore proves
// its numbers"): every figure comes from the generated FundBalance /
// AccountBalance / PurposeBalance queries - the exact three GET
// /api/balances composes for the home screen through internal/ledger, and
// the exact three Restore's verifyTotals recomputes against. It does not
// sum, subtract or otherwise re-derive a balance of its own; q is the
// export's one read transaction, so these figures and the rows beside them
// are the same instant.
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
// canonical document's byte order (TestGoldenFixtureMatchesExport). Screens
// read names in the order the treasurer reads them; a backup only needs an
// order that never changes, and ids already give it one.
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
