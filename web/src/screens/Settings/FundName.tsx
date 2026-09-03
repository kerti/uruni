import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { getFund, renameFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Fund } from '@/lib/setup'

const text = copy.settings.fund

/**
 * Renaming the kas (M6.15). The setup wizard already promises this - "bisa
 * diganti nanti kalau perlu" - and until now nothing delivered it.
 *
 * The name is a display label: it heads every screen and the public report,
 * and nothing posted references it, so a rename rewrites no history. What
 * it does not touch is the report's slug, which is the address she may
 * already have shared; rotating that is its own decision, not a side effect
 * of fixing a typo.
 *
 * `onRenamed` exists because the fund's name is also Shell's header, which
 * App.tsx read once on mount - without it the header would keep showing the
 * old name until a reload.
 */
export default function FundName({ onRenamed }: { onRenamed: (fund: Fund) => void }) {
  const [loadState, loadRun] = useApi<Fund>()
  const [saveState, saveRun] = useApi<Fund>()
  // null means "she has not typed yet", so the field shows the loaded name.
  // Derived rather than copied into state by an effect: an effect would
  // leave one render where the input exists and is still empty, which is a
  // blank field on screen and a race in a test.
  const [typed, setTyped] = useState<string | null>(null)

  useEffect(() => {
    void loadRun(getFund)
  }, [loadRun])

  const loadedName = loadState.data?.name
  const name = typed ?? loadedName ?? ''

  const saving = saveState.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '' || trimmed === loadedName) return
    void saveRun(async () => {
      const updated = await renameFund(trimmed)
      onRenamed(updated)
      return updated
    })
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {loadState.status === 'idle' || loadState.status === 'loading' ? (
        <Loading />
      ) : loadState.status === 'error' ? (
        loadState.error && <ErrorState error={loadState.error} onRetry={() => void loadRun(getFund)} />
      ) : (
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="fund-name">{text.nameLabel}</Label>
            <Input id="fund-name" type="text" value={name} onChange={(event) => setTyped(event.target.value)} />
          </div>
          <Button type="submit" className="h-11 self-start" disabled={saving || name.trim() === '' || name.trim() === loadedName}>
            {saving ? text.saving : text.save}
          </Button>
          {saveState.status === 'success' && (
            <p role="status" className="text-sm text-success">
              {text.saved}
            </p>
          )}
          {saveState.status === 'error' && saveState.error && <ErrorState error={saveState.error} />}
        </form>
      )}
    </section>
  )
}
