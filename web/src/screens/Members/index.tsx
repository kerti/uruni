import Roster from '@/screens/Members/Roster'
import { copy } from '@/copy/id'

/**
 * The roster screen: every member of the fund.
 *
 * Its own destination rather than a section of Pengaturan: a fund's whole
 * membership is a list, not a setting, and would bury locations and titipan
 * under a scroll of names.
 *
 * The tiers that price dues live in Pengaturan: naming and pricing a tier is
 * rare admin next to an everyday list, now that the roster row shows the
 * tier's effect. With the two on separate routes there is nothing to
 * synchronise: leaving Pengaturan for Anggota remounts the roster, which
 * loads members and tiers together.
 */
export default function Members() {
  return (
    // gap-6, Pengaturan's title-to-first-section spacing, so the two tab
    // screens open the same way.
    <div className="flex flex-col gap-6">
      <h1 className="text-xl font-semibold">{copy.members.heading}</h1>

      <Roster />
    </div>
  )
}
