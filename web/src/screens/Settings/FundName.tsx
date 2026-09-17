import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { getFund, renameFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Fund } from '@/lib/setup'

const text = copy.settings.fund

/**
 * Renaming the kas (M6.15, converted to the card-plus-dialog shape in M6.30 -
 * ADR-032 "Every non-posting edit is a dialog"): the name reads as a card,
 * and tapping it opens the rename dialog at `?edit=fund:name`.
 *
 * The setup wizard already promises this - "bisa diganti nanti kalau perlu" -
 * and until M6.15 nothing delivered it.
 *
 * The name is a display label: it heads every screen and the public report,
 * and nothing posted references it, so a rename rewrites no history. What it
 * does not touch is the report's slug, which is the address she may already
 * have shared; rotating that is its own decision, not a side effect of fixing
 * a typo.
 *
 * `onRenamed` exists because the fund's name is also Shell's header, which
 * App.tsx read once on mount - without it the header would keep showing the
 * old name until a reload.
 *
 * There is exactly one fund, so this section's dialog has no id to carry and
 * no list to look one up in: `fund:name` is its only value, and `new` is a
 * value it never opens. An `?edit=fund:<anything else>` arriving by hand is
 * cleared once, the same as a dead id in any other section.
 */
export default function FundName({ onRenamed }: { onRenamed: (fund: Fund) => void }) {
  const [loadState, loadRun] = useApi<Fund>()
  const { value, open, close, clear } = useDialogParam()

  useEffect(() => {
    void loadRun(getFund)
  }, [loadRun])

  const fund = loadState.data ?? null

  // 'fund' is this section's own prefix (dialogTarget.ts). The rename dialog
  // is addressed by name rather than by id, so it lands as `invalid` on that
  // parser - this section's one owned value, told apart from a real dead
  // link by comparing the raw string.
  const target = parseDialogTarget('fund', value)
  const isOwn = target.kind !== 'foreign'
  const editing = value === 'fund:name'

  // A `fund:` value this section cannot use: strip it once, rather than
  // leave a param no dialog answers to. Never a `foreign` value - that
  // belongs to a sibling section on this same screen. `clear`, not `close`,
  // for the reason Locations documents: a dead link is not a navigation
  // worth undoing.
  useEffect(() => {
    if (!isOwn || editing) return
    clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, isOwn, editing])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {loadState.status === 'idle' || loadState.status === 'loading' ? (
        <Loading />
      ) : loadState.status === 'error' || fund === null ? (
        loadState.error && <ErrorState error={loadState.error} onRetry={() => void loadRun(getFund)} />
      ) : (
        <button
          type="button"
          aria-label={text.editAria(fund.name)}
          onClick={() => open('fund:name')}
          className="flex min-h-11 w-full items-center rounded-lg bg-card px-4 py-3 text-left ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
        >
          <span className="min-w-0 truncate font-medium">{fund.name}</span>
        </button>
      )}

      <EditFundNameDialog
        fund={fund}
        open={editing && fund !== null}
        onClose={close}
        onRenamed={(updated) => {
          onRenamed(updated)
          close()
          void loadRun(getFund)
        }}
      />
    </section>
  )
}

/**
 * The rename dialog. `fund` is null only while closing (the same window
 * EditLocationDialog documents), so the last known name stays on screen
 * rather than the field blanking mid-close.
 */
function EditFundNameDialog({
  fund,
  open,
  onClose,
  onRenamed,
}: {
  fund: Fund | null
  open: boolean
  onClose: () => void
  onRenamed: (fund: Fund) => void
}) {
  const [state, run] = useApi<Fund>()
  const [name, setName] = useState('')

  // Reset to the fund's current name each time the dialog opens, so a
  // cancelled edit never leaves its text waiting for the next one.
  useEffect(() => {
    if (open && fund !== null) setName(fund.name)
  }, [open, fund])

  const busy = state.status === 'loading'
  const trimmed = name.trim()
  const unchanged = fund !== null && trimmed === fund.name

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '' || unchanged) return
    void run(async () => {
      const updated = await renameFund(trimmed)
      onRenamed(updated)
      return updated
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.editTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="fund-name">{text.nameLabel}</Label>
            <Input id="fund-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={busy || trimmed === '' || unchanged}>
              {busy ? text.saving : text.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
