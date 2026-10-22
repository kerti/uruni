import User from '@/screens/Settings/User'
import Backup from '@/screens/Settings/Backup'
import DuesTiers from '@/screens/Settings/DuesTiers'
import FundName from '@/screens/Settings/FundName'
import Incidentals from '@/screens/Settings/Incidentals'
import Locations from '@/screens/Settings/Locations'
import PassThrough from '@/screens/Settings/PassThrough'
import ReportLink from '@/screens/Settings/ReportLink'
import SectionDivider from '@/components/SectionDivider'
import { copy } from '@/copy/id'
import type { Fund } from '@/lib/setup'

/**
 * The settings screen: one bundled "Pengaturan" destination with a section per
 * thing, rather than separate top-level screens, with the fund's name at the
 * top. None of these is an everyday action, and one screen with sections is
 * fewer moving parts than several settings destinations in the footer nav.
 *
 * Each section owns its data and its writes; this file is the frame and the
 * order, nothing else.
 *
 * Every section is a card list whose editing happens in a dialog (ADR-032
 * "Every non-posting edit is a dialog"). With the inline forms gone, the
 * screen is short enough that stacked sections beat tabs, which is what
 * ADR-032's refusal of tabs on the admin screens rests on: tabs would hide
 * half of a screen she visits rarely, where she has no muscle memory for what
 * is behind the second panel.
 *
 * The sections share one URL and several of them own an `?edit=` dialog, so each
 * parses that param against its own prefix and leaves every other value alone
 * - see lib/dialogTarget.ts for why that is load-bearing.
 *
 * Golongan & tarif lives here (moved from Anggota, see screens/Members/index.tsx).
 * Incidentals (ADR-032) sits beside Titipan - the two are kinds of one
 * `purpose` (CONTEXT.md). Laporan publik (ADR-035) comes after the admin
 * sections, then Akun bendahara (#386): it is about the login, not the fund,
 * and changed rarer still, so it sits below everything about the kas but
 * above Cadangan (ADR-012), which stays last: downloading the whole fund is
 * rarer than any admin above it, and it is the one section with no dialog and
 * nothing to browse.
 *
 * No back control: Shell's footer is how every screen is left, and a second
 * way out would be one affordance too many.
 */
export default function Settings({
  fund,
  onFundChanged,
  refreshFund,
}: {
  fund: Fund
  onFundChanged: (fund: Fund) => void
  refreshFund: () => Promise<void>
}) {
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-xl font-semibold">{copy.settings.heading}</h1>

      {/* A hairline between each independent section (#319); gap-6 rather
          than gap-8, since the line now does part of the separating. */}
      <FundName fund={fund} onChanged={onFundChanged} refresh={refreshFund} />
      <SectionDivider />
      <Locations />
      <SectionDivider />
      <PassThrough />
      <SectionDivider />
      <Incidentals />
      <SectionDivider />
      <DuesTiers />
      <SectionDivider />
      <ReportLink fund={fund} onChanged={onFundChanged} refresh={refreshFund} />
      <SectionDivider />
      <User />
      <SectionDivider />
      <Backup />
    </div>
  )
}
