// The closed sets the schema's CHECK constraints define
// (internal/db/migrations/00001_schema.sql). The wire carries them as plain
// strings; these unions name them, and the golden samples in test/wire hold
// them to real responses.

/** transaction.direction */
export type Direction = 'in' | 'out'

/** transaction.kind */
export type TransactionKind = 'opening' | 'normal' | 'dues' | 'reimbursement' | 'adjustment' | 'transfer'

/** account.kind */
export type AccountKind = 'cash' | 'bank'

/** purpose.kind */
export type PurposeKind = 'main' | 'incidental' | 'pass_through'

/** transfer.kind */
export type TransferKind = 'between_accounts' | 'reclass_purpose'

/** reconciliation_line.resolution */
export type Resolution = 'matched' | 'entry_added' | 'adjusted' | 'left_open'
