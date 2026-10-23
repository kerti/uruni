import { useEffect } from 'react'

import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { getDuesGrid } from '@/lib/dues'
import { formatPeriod } from '@/lib/dates'
import { useApi } from '@/lib/useApi'
import type { DuesGrid, DuesGridCell } from '@/lib/dues'

const text = copy.dues
const gridText = copy.dues.grid

/** The word for one cell: the existing status words, plus the grid's own for no
 * obligation. */
const cellWord: Record<DuesGridCell, string> = {
  paid: text.statuses.paid,
  partial: text.statuses.partial,
  unpaid: text.statuses.unpaid,
  none: gridText.none,
}

/**
 * One cell's shape, drawn (never a typed glyph): filled = Lunas, half-filled =
 * Bayar sebagian, hollow = Belum bayar, a dash = no obligation. The shape carries
 * the status as much as the colour does, so the grid reads in greyscale
 * (ADR-044). Gentle tones only: green family, terracotta, neutral - never red.
 */
function CellShape({ cell }: { cell: DuesGridCell }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 14 14" className="mx-auto block size-3.5" focusable="false">
      {cell === 'paid' && <circle cx="7" cy="7" r="6" className="fill-success" />}
      {cell === 'partial' && (
        <>
          <circle cx="7" cy="7" r="5.5" className="fill-none stroke-attention" strokeWidth="1.5" />
          <path d="M7 1.5 A5.5 5.5 0 0 0 7 12.5 Z" className="fill-attention" />
        </>
      )}
      {cell === 'unpaid' && <circle cx="7" cy="7" r="5.5" className="fill-none stroke-muted-foreground" strokeWidth="1.5" />}
      {cell === 'none' && (
        <line x1="3.5" y1="7" x2="10.5" y2="7" className="stroke-muted-foreground" strokeWidth="1.5" strokeLinecap="round" />
      )}
    </svg>
  )
}

const legendCells: readonly DuesGridCell[] = ['paid', 'partial', 'unpaid', 'none']

/** Groups consecutive periods by year, for the row of years above the heads:
 * the first column of the grid and every January start a group. */
function yearGroups(periods: string[]): { year: string; span: number }[] {
  const groups: { year: string; span: number }[] = []
  for (const period of periods) {
    const year = period.slice(0, 4)
    const last = groups[groups.length - 1]
    if (last && last.year === year) last.span += 1
    else groups.push({ year, span: 1 })
  }
  return groups
}

/**
 * The twelve-month table (ADR-044): members down the side (name pinned left, in
 * the API's order), months across, oldest left. Status only - no counts, totals,
 * amounts, sorting or controls; the cells are not buttons. Each cell carries its
 * month and status as visually hidden text.
 */
export function DuesGridTable({ grid }: { grid: DuesGrid }) {
  const groups = yearGroups(grid.periods)
  const startsYear = (index: number) => index === 0 || grid.periods[index].slice(5) === '01'

  return (
    <div className="flex flex-col gap-3">
      <table className="w-full table-fixed border-collapse">
        <colgroup>
          <col className="w-24" />
          {grid.periods.map((period) => (
            <col key={period} />
          ))}
        </colgroup>
        <thead>
          <tr aria-hidden="true">
            <td />
            {groups.map((group) => (
              <td
                key={group.year}
                colSpan={group.span}
                className="border-l border-border pb-0.5 pl-0.5 text-left text-[10px] text-muted-foreground"
              >
                <span className="tabular whitespace-nowrap">{group.year}</span>
              </td>
            ))}
          </tr>
          <tr>
            <td />
            {grid.periods.map((period, index) => (
              <th
                key={period}
                scope="col"
                className={`pb-1 text-center text-[10px] font-normal text-muted-foreground ${startsYear(index) ? 'border-l border-border' : ''}`}
              >
                <span aria-hidden="true">{gridText.monthHeads[Number(period.slice(5)) - 1]}</span>
                <span className="sr-only">{formatPeriod(period)}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {grid.members.map((member) => (
            <tr key={member.id} className="border-t border-border">
              <th scope="row" className="truncate py-2 pr-2 text-left text-sm font-medium">
                {member.name}
              </th>
              {member.cells.map((cell, index) => (
                <td key={grid.periods[index]} className={`py-2 ${startsYear(index) ? 'border-l border-border' : ''}`}>
                  <CellShape cell={cell} />
                  <span className="sr-only">{gridText.cellLabel(formatPeriod(grid.periods[index]), cellWord[cell])}</span>
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>

      <ul className="flex flex-wrap gap-x-4 gap-y-1.5 text-sm text-muted-foreground">
        {legendCells.map((cell) => (
          <li key={cell} className="flex items-center gap-1.5">
            <span className="w-3.5">
              <CellShape cell={cell} />
            </span>
            {cellWord[cell]}
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * The grid view of the dues status screen: fetches GET /api/dues-status/grid and
 * draws it. `refetchKey` is the screen's "a write happened elsewhere" signal.
 */
export default function DuesStatusGrid({ refetchKey }: { refetchKey?: unknown }) {
  const [state, run] = useApi<DuesGrid>()

  useEffect(() => {
    void run(getDuesGrid)
  }, [run, refetchKey])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-0.5">
        <h2 className="text-lg font-semibold">{gridText.heading}</h2>
        {state.status === 'success' && state.data && state.data.periods.length > 0 && (
          <p className="text-sm text-muted-foreground">
            {gridText.range(formatPeriod(state.data.periods[0]), formatPeriod(state.data.periods[state.data.periods.length - 1]))}
          </p>
        )}
      </div>

      {(state.status === 'idle' || state.status === 'loading') && <Loading />}
      {state.status === 'error' && state.error && <ErrorState error={state.error} onRetry={() => void run(getDuesGrid)} />}
      {state.status === 'success' &&
        state.data &&
        (state.data.members.length === 0 ? <p className="text-muted-foreground">{gridText.empty}</p> : <DuesGridTable grid={state.data} />)}
    </section>
  )
}
