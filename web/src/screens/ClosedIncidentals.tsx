import { CaretRight } from '@phosphor-icons/react'
import { useEffect } from 'react'

import BackLink from '@/components/BackLink'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { listIncidentals } from '@/lib/incidentals'
import { useApi } from '@/lib/useApi'
import type { Incidental } from '@/lib/incidentals'
import { tappableCard } from '@/components/cardClasses'

const text = copy.incidentals

/** A closed envelope's card box. */
const cardClass = `flex items-center justify-between gap-3 ${tappableCard}`

/**
 * Every closed envelope, most recently closed first - the screen behind
 * Beranda's "Amplop yang sudah ditutup" row (ADR-047). A screen rather than a
 * dialog because the list is unbounded, and one list rather than tabs: ADR-032
 * retired the open/all tab screen. Each card opens the envelope's detail,
 * where its record and Buka lagi both are (ADR-031).
 */
export default function ClosedIncidentals({ onBack, onOpen }: { onBack: () => void; onOpen: (purposeId: number) => void }) {
  const [state, run] = useApi<Incidental[]>()

  useEffect(() => {
    void run(() => listIncidentals(false))
  }, [run])

  const closed = (state.data ?? [])
    .filter((envelope): envelope is Incidental & { closed_on: string } => envelope.closed_on !== null)
    .sort((a, b) => (a.closed_on < b.closed_on ? 1 : a.closed_on > b.closed_on ? -1 : b.created_at - a.created_at))

  return (
    <div className="flex w-full flex-col gap-4">
      <BackLink label={text.detail.backToHome} onClick={onBack} />
      <h1 className="text-xl font-semibold">{text.closedRow}</h1>

      {state.status === 'idle' || state.status === 'loading' ? (
        <Loading />
      ) : state.status === 'error' || !state.data ? (
        state.error && <ErrorState error={state.error} onRetry={() => void run(() => listIncidentals(false))} />
      ) : closed.length === 0 ? (
        <p className="text-muted-foreground">{text.closedEmpty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {closed.map((envelope) => (
            <li key={envelope.purpose_id}>
              <button type="button" onClick={() => onOpen(envelope.purpose_id)} className={cardClass}>
                <span className="sr-only">{text.cardVerb}</span>{' '}
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium">{envelope.occasion}</span>
                  <span className="text-sm text-muted-foreground">{text.closedOn(formatIsoDate(envelope.closed_on))}</span>
                </span>
                <CaretRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
