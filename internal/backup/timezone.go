package backup

import "github.com/kerti/uruni/internal/tz"

// jakarta is the treasurer's calendar day, shared with the ledger's report
// through internal/tz (ADR-035): a dump's date is stamped in Asia/Jakarta,
// never the server's own clock. See tz.Jakarta for why it is fixed.
var jakarta = tz.Jakarta
