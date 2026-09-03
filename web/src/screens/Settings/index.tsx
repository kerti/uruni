import FundName from '@/screens/Settings/FundName'
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
    </div>
  )
}
