// The one rule for every location picker: blank, unless there is exactly one
// possible answer. Shared by every form that asks where the money is - Catat
// (Masuk, Keluar, Pindah pos, both sides of Pindah lokasi), Catat iuran, Bayar
// talangan, Tutup amplop.
//
// Nothing is remembered between forms. A pre-filled last-used location (#737)
// let a dues payment into the bank be saved as cash with no conscious choice,
// and a wrong location is the error cek kas exists to catch: it leaves two
// locations off, and only a reversal undoes it. One more tap makes her say
// where this money is, each time.

import type { Account } from '@/lib/accounts'

/** Blank, unless exactly one candidate exists - then that one is the only
 * possible answer. */
export function soleCandidateId(candidates: { id: number }[]): number | null {
  return candidates.length === 1 ? candidates[0].id : null
}

/** A location picker's starting value: the fund's only active location, else
 * none. A retired location is never a candidate. */
export function defaultAccountId(accounts: Account[]): number | null {
  return soleCandidateId(accounts.filter((a) => a.inactive_on === null))
}
