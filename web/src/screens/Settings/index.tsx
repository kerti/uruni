import AppVersion from '@/screens/Settings/AppVersion'
import DuesTiers from '@/screens/Settings/DuesTiers'
import FundName from '@/screens/Settings/FundName'
import Incidentals from '@/screens/Settings/Incidentals'
import Locations from '@/screens/Settings/Locations'
import PassThrough from '@/screens/Settings/PassThrough'
import { copy } from '@/copy/id'
import type { Fund } from '@/lib/setup'

/**
 * The settings screen (M6.15): one bundled "Pengaturan" destination with a
 * section per thing, rather than four separate top-level screens for
 * locations, pass-through purposes, the roster (M6.16) and dues tiers
 * (M6.17), with the fund's own name at the top of it. None of these is an
 * everyday action, and one screen with sections is fewer moving parts than
 * four settings destinations in the footer nav.
 *
 * Each section owns its own data and its own writes; this file is the frame
 * and the order, nothing else. M6.16 and M6.17 add their sections here.
 *
 * Every section is a card list whose editing happens in a dialog (M6.28 for
 * Lokasi, M6.30 for the rest - ADR-032 "Every non-posting edit is a
 * dialog"). With the inline forms gone, the screen is short enough that
 * stacked sections beat tabs, which is what ADR-032's refusal of tabs on the
 * admin screens rests on: tabs would hide half of a screen she visits rarely,
 * where she has no muscle memory for what is behind the second panel.
 *
 * The sections share one URL and four of them own an `?edit=` dialog, so each
 * one parses that param against its own prefix and leaves every other value
 * alone - see lib/dialogTarget.ts for why that is load-bearing.
 *
 * Golongan & tarif (#232, M6.31): moved here from Anggota, superseding
 * M6.16/M6.17's placement - see screens/Members/index.tsx for why that
 * argument stopped holding. It sits last: it is the rarest admin on a screen
 * of rare admin.
 *
 * Incidentals (#263, ADR-032): opening a new envelope lost its home when
 * Beranda's purpose breakdown became entry points only, and it lands here,
 * beside Titipan - the two are kinds of one `purpose` (CONTEXT.md).
 *
 * No back control: Shell's footer is how every screen is left now, and a
 * second way out would be one affordance too many.
 */
export default function Settings({ onFundRenamed }: { onFundRenamed: (fund: Fund) => void }) {
  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-xl font-semibold">{copy.settings.heading}</h1>

      <FundName onRenamed={onFundRenamed} />
      <Locations />
      <PassThrough />
      <Incidentals />
      <DuesTiers />
      <AppVersion />
    </div>
  )
}
