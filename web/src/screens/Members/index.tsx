import Roster from '@/screens/Members/Roster'
import { copy } from '@/copy/id'

/**
 * The roster screen (M6.16): every member of the fund.
 *
 * Its own destination rather than a section of Pengaturan, which is where
 * the issue originally put it. A fund's whole membership is a list, not a
 * setting - dropped into the settings screen it would bury locations and
 * titipan under a scroll of names.
 *
 * The tiers that price dues used to sit below the roster here, on the
 * argument that "a member's tier is set on the member, so the two are read
 * and edited in one sitting". M6.31 (#232) moved them to Pengaturan: that
 * argument stops holding once the roster row shows the tier's effect (#233),
 * and naming and pricing a tier is rare admin next to an everyday list.
 *
 * `tiersVersion` went with them. It existed only because the tier section
 * below wrote the rows the roster's picker read, so a rename left the picker
 * stale until the screen was left and re-entered. With the two on separate
 * routes there is nothing to synchronise: leaving Pengaturan for Anggota
 * remounts the roster, which loads members and tiers together.
 */
export default function Members() {
  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-xl font-semibold">{copy.members.heading}</h1>

      <Roster />
    </div>
  )
}
