// Typed call over apiFetch for GET /api/accounts (M6.8), same idiom as
// lib/auth.ts and lib/setup.ts. The Account shape is already defined in
// lib/setup.ts (the setup wizard reads the same rows POST /api/setup just
// created) - re-exported here rather than redefined, so the two modules
// can't drift apart on the same wire shape.

import { apiFetch } from '@/lib/api'
import type { Account } from '@/lib/setup'

export type { Account }

/**
 * GET /api/accounts - every location the fund has, active and retired alike
 * (internal/http/accounts.go's listAccounts: history still needs the
 * retired ones). Callers that must offer only a place to *record against*
 * - this screen's AccountPicker - filter out `inactive_on` rows themselves;
 * this module stays a plain wire call with no policy of its own.
 */
export function listAccounts(): Promise<Account[]> {
  return apiFetch<Account[]>('/api/accounts')
}
