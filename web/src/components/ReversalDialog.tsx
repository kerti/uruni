import { useState, type FormEvent } from 'react'

import ReversalDateLine from '@/components/ReversalDateLine'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import type { ApiError } from '@/lib/api'
import { reverseTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { transactionReversalCopy, transactionReversalErrorText, type ReversalCopy } from '@/components/reversalCopy'
import type { Transaction } from '@/lib/transactions'

/**
 * Reversing one ordinary transaction, as a dialog (ADR-046, ADR-042).
 *
 * Reason (optional) and a yes/no: the date line states when the entry will be
 * dated, and it asks no date and no amount - the ledger copies both off the
 * reversed row. It posts, so a tap outside does nothing (`posts`); the X,
 * Escape and "Tidak jadi" close it. Opened from `?edit=reverse:<id>` by the
 * caller, after the entry detail has closed, so no two dialogs are ever open
 * at once.
 *
 * `transaction` is null only while the dialog closes. The body is its own
 * component so the reason and the error start empty every time it opens.
 */
export default function ReversalDialog({
  transaction,
  open,
  onClose,
  onReversed,
  refresh,
  words = transactionReversalCopy,
  errorText = transactionReversalErrorText,
}: {
  transaction: Transaction | null
  open: boolean
  onClose: () => void
  onReversed: () => void
  refresh?: () => Promise<void> | void
  words?: ReversalCopy
  errorText?: (err: ApiError) => string
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close} posts>
        {transaction !== null && (
          <ReversalBody
            transaction={transaction}
            onClose={onClose}
            onReversed={onReversed}
            refresh={refresh}
            words={words}
            errorText={errorText}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function ReversalBody({
  transaction,
  onClose,
  onReversed,
  refresh,
  words,
  errorText,
}: {
  transaction: Transaction
  onClose: () => void
  onReversed: () => void
  refresh?: () => Promise<void> | void
  words: ReversalCopy
  errorText: (err: ApiError) => string
}) {
  const [note, setNote] = useState('')
  const [state, run] = useApi<Transaction>()

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    void run(
      async () => {
        const trimmed = note.trim()
        // A blank reason sends null, never a generated default: the reversal
        // explains itself through the list's display label.
        const result = await reverseTransaction(transaction.id, trimmed === '' ? null : trimmed)
        onReversed()
        return result
      },
      { settle: refresh },
    )
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>{words.title}</DialogTitle>
      </DialogHeader>
      <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
        <div className="flex flex-col gap-1">
          {words.explainer && <p className="text-sm text-muted-foreground">{words.explainer}</p>}
          <ReversalDateLine transaction={transaction} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`reversal-note-${transaction.id}`}>{words.noteLabel}</Label>
          <Input
            id={`reversal-note-${transaction.id}`}
            type="text"
            className="h-11"
            value={note}
            onChange={(event) => setNote(event.target.value)}
            disabled={busy}
          />
        </div>

        {state.status === 'error' && state.error && (
          <p role="alert" className="text-sm text-attention-text">
            {errorText(state.error)}
          </p>
        )}

        <DialogFooter className="mt-1">
          <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
            {words.cancel}
          </Button>
          <Button type="submit" size="lg" disabled={busy}>
            {busy ? words.submitting : words.confirm}
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}
