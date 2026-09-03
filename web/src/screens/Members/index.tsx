import { useState } from 'react'

import DuesTiers from '@/screens/Members/DuesTiers'
import Roster from '@/screens/Members/Roster'
import { copy } from '@/copy/id'

/**
 * The roster screen (M6.16 + M6.17): every member of the fund, and the
 * tiers that price their dues.
 *
 * Its own destination rather than two more sections of Pengaturan, which is
 * where the issues originally put it. A fund's whole membership is a list,
 * not a setting - dropped into the settings screen it would bury locations
 * and titipan under a scroll of names. Roster and tiers belong together for
 * the opposite reason: a member's tier is set on the member, so the two are
 * read and edited in one sitting.
 *
 * The tiers are here rather than beside the dues status view (M6.12) for
 * the same reason. Status is a monthly reading; naming and pricing a tier is
 * rare admin, and the roster is what it acts on.
 *
 * Which is exactly why this screen owns `tiersVersion`. The roster's tier
 * picker reads the same rows the section below it writes, and each section
 * loads its own data - so renaming a tier left the picker showing the old
 * name until the screen was left and re-entered. The counter is the one
 * thing the two sections share: DuesTiers bumps it after any tier write,
 * Roster re-reads when it changes. A number rather than a callback carrying
 * the new list, so neither section has to know the other's shape.
 */
export default function Members() {
  const [tiersVersion, setTiersVersion] = useState(0)

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-xl font-semibold">{copy.members.heading}</h1>

      <Roster tiersVersion={tiersVersion} />
      <DuesTiers onTiersChanged={() => setTiersVersion((version) => version + 1)} />
    </div>
  )
}
