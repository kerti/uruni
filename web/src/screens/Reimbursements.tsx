import { useEffect, useState, type FormEvent } from 'react'

import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import MemberPicker from '@/components/pickers/MemberPicker'
import PurposePicker from '@/components/pickers/PurposePicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import {
  listReimbursements,
  createReimbursement,
  updateReimbursement,
  deleteReimbursement,
  settleReimbursement,
} from '@/lib/reimbursements'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Member } from '@/lib/setup'
import type { Purpose } from '@/lib/purposes'
import type { Reimbursement } from '@/lib/reimbursements'
import { listMembers } from '@/lib/setup'

const text = copy.reimbursements

/** Local YYYY-MM-DD — same helper as RecordTransaction.tsx. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

interface FormData {
  members: Member[]
  purposes: Purpose[]
  accounts: Account[]
}

/**
 * The reimbursements screen (M6.18, PRD §7.4): record that a member
 * fronted money, settle when repaid, waive when the member forgives the
 * debt ("putihkan"), or correct/remove a claim entered wrongly — only
 * until settled, after which the payout is a posted ledger row.
 *
 * Two-tab view: outstanding (default) vs all. A link on the home screen
 * navigates here; onBack returns to home.
 */
export default function Reimbursements({ onBack }: { onBack: () => void }) {
  const [listState, listRun] = useApi<Reimbursement[]>()
  const [formDataState, formDataRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  const [tab, setTab] = useState<'outstanding' | 'all'>('outstanding')
  const [showRecordForm, setShowRecordForm] = useState(false)
  const [settleId, setSettleId] = useState<number | null>(null)
  const [correctId, setCorrectId] = useState<number | null>(null)
  const [deleteId, setDeleteId] = useState<number | null>(null)

  const [notice, setNotice] = useState<string | null>(null)

  function fetchList() {
    void listRun(() => listReimbursements(tab === 'outstanding'))
  }

  function fetchFormData() {
    return formDataRun(async () => {
      const [members, purposes, accounts] = await Promise.all([listMembers(), listPurposes(), listAccounts()])
      return { members, purposes, accounts }
    })
  }

  useEffect(() => {
    void fetchList()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab])

  useEffect(() => {
    void fetchFormData()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formDataRun])

  // Close inline forms when the list refreshes (after a successful action).
  useEffect(() => {
    if (listState.status === 'success' && notice) {
      setSettleId(null)
      setCorrectId(null)
      setDeleteId(null)
      setShowRecordForm(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listState.status])

  const submitting = submitState.status === 'loading'

  function handleRecordClaim(memberId: number, purposeId: number, amount: number, occurredOn: string, note: string) {
    void submitRun(async () => {
      await createReimbursement({
        member_id: memberId,
        purpose_id: purposeId,
        amount,
        incurred_on: occurredOn,
        note: note === '' ? null : note,
      })
      setNotice(text.record.success)
      setTab('outstanding')
      fetchList()
    })
  }

  function handleSettle(id: number, accountId: number, occurredOn: string) {
    void submitRun(async () => {
      await settleReimbursement(id, { account_id: accountId, occurred_on: occurredOn })
      setNotice(text.settle.success)
      fetchList()
    })
  }

  function handleCorrect(id: number, patch: { member_id?: number; purpose_id?: number; amount?: number; incurred_on?: string; note?: string | null }) {
    void submitRun(async () => {
      await updateReimbursement(id, patch)
      setNotice(text.correct.success)
      fetchList()
    })
  }

  function handleWaive(id: number) {
    void submitRun(async () => {
      await updateReimbursement(id, { waived_on: todayISODate() })
      fetchList()
    })
  }

  function handleUnwaive(id: number) {
    void submitRun(async () => {
      await updateReimbursement(id, { waived_on: null })
      fetchList()
    })
  }

  function handleDelete(id: number) {
    void submitRun(async () => {
      await deleteReimbursement(id)
      fetchList()
    })
  }

  if (listState.status === 'idle' || listState.status === 'loading') {
    return <Loading />
  }

  if (listState.status === 'error' || !listState.data) {
    return listState.error ? <ErrorState error={listState.error} onRetry={() => void listRun(() => listReimbursements(tab === 'outstanding'))} /> : null
  }

  const claims = listState.data
  const fd = formDataState.data

  const memberNames = fd ? new Map(fd.members.map((m) => [m.id, m.name])) : new Map()
  const purposeNames = fd ? new Map(fd.purposes.map((p) => [p.id, p.name])) : new Map()

  return (
    <div className="mx-auto flex w-full max-w-sm flex-col gap-4">
      <h1 className="text-2xl font-semibold">{text.heading}</h1>
      <p className="text-sm text-muted-foreground">{text.body}</p>

      {notice && (
        <p role="status" className="rounded-lg bg-success-soft px-3 py-2 text-sm text-success">
          {notice}
        </p>
      )}

      {/* Tab bar */}
      <div role="tablist" aria-label={text.heading} className="grid grid-cols-2 gap-2">
        <Button
          type="button"
          variant={tab === 'outstanding' ? 'default' : 'outline'}
          aria-pressed={tab === 'outstanding'}
          className="h-11"
          onClick={() => { setTab('outstanding'); setNotice(null) }}
        >
          {text.outstandingTab}
        </Button>
        <Button
          type="button"
          variant={tab === 'all' ? 'default' : 'outline'}
          aria-pressed={tab === 'all'}
          className="h-11"
          onClick={() => { setTab('all'); setNotice(null) }}
        >
          {text.allTab}
        </Button>
      </div>

      {/* Record claim button */}
      {!showRecordForm && (
        <Button type="button" className="h-11" onClick={() => setShowRecordForm(true)}>
          Catat penggantian
        </Button>
      )}

      {/* Record claim form */}
      {showRecordForm && fd && (
        <RecordClaimForm
          members={fd.members}
          purposes={fd.purposes}
          onSubmit={handleRecordClaim}
          onCancel={() => setShowRecordForm(false)}
          submitting={submitting}
        />
      )}

      {/* Claim list */}
      {claims.length === 0 ? (
        <p className="text-muted-foreground">
          {tab === 'outstanding' ? text.emptyOutstanding : text.emptyAll}
        </p>
      ) : (
        <ul className="flex flex-col gap-3">
          {claims.map((claim) => (
            <li key={claim.id} className="flex flex-col gap-2 rounded-2xl bg-card p-4 ring-1 ring-foreground/10">
              <div className="flex items-start justify-between gap-3">
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium">{memberNames.get(claim.member_id) ?? '—'}</span>
                  <span className="truncate text-sm text-muted-foreground">{purposeNames.get(claim.purpose_id) ?? '—'}</span>
                </span>
                <span className="tabular shrink-0 font-medium">{formatIDR(claim.amount)}</span>
              </div>

              <div className="flex items-center justify-between text-sm text-muted-foreground">
                <span>{formatIsoDate(claim.incurred_on)}</span>
                <StatusBadge claim={claim} tab={tab} />
              </div>

              {claim.note && <p className="text-sm text-muted-foreground">{claim.note}</p>}

              {/* Actions — only on the outstanding tab; settled claims show no actions */}
              {tab === 'outstanding' && !claim.waived_on && (
                <div className="flex flex-wrap gap-2 pt-1">
                  {settleId !== claim.id && correctId !== claim.id && (
                    <>
                      <Button type="button" size="sm" onClick={() => { setSettleId(claim.id); setCorrectId(null); setDeleteId(null) }}>
                        {text.actions.settle}
                      </Button>
                      <Button type="button" size="sm" variant="outline" onClick={() => { setCorrectId(claim.id); setSettleId(null); setDeleteId(null) }}>
                        {text.actions.correct}
                      </Button>
                      <Button type="button" size="sm" variant="outline" onClick={() => handleWaive(claim.id)} disabled={submitting}>
                        {text.actions.waive}
                      </Button>
                      {deleteId !== claim.id && (
                        <Button type="button" size="sm" variant="ghost" className="text-destructive" onClick={() => setDeleteId(claim.id)}>
                          {text.actions.delete}
                        </Button>
                      )}
                    </>
                  )}
                </div>
              )}

              {/* Un-waive action for waived claims */}
              {tab === 'outstanding' && claim.waived_on && (
                <div className="flex gap-2 pt-1">
                  <Button type="button" size="sm" variant="outline" onClick={() => handleUnwaive(claim.id)} disabled={submitting}>
                    {text.actions.unwaive}
                  </Button>
                </div>
              )}

              {/* Inline settle form */}
              {settleId === claim.id && fd && (
                <SettleForm
                  accounts={fd.accounts}
                  claimAmount={claim.amount}
                  onSubmit={(accountId, occurredOn) => handleSettle(claim.id, accountId, occurredOn)}
                  onCancel={() => setSettleId(null)}
                  submitting={submitting}
                />
              )}

              {/* Inline correct form */}
              {correctId === claim.id && fd && (
                <CorrectForm
                  members={fd.members}
                  purposes={fd.purposes}
                  claim={claim}
                  onSubmit={(patch) => handleCorrect(claim.id, patch)}
                  onCancel={() => setCorrectId(null)}
                  submitting={submitting}
                />
              )}

              {/* Delete confirmation */}
              {deleteId === claim.id && (
                <div className="flex flex-col gap-2 rounded-lg bg-attention-soft p-3">
                  <p className="text-sm">{text.actions.delete}?</p>
                  <div className="flex gap-2">
                    <Button type="button" size="sm" variant="destructive" onClick={() => handleDelete(claim.id)} disabled={submitting}>
                      {submitting ? text.actions.deleting : text.actions.delete}
                    </Button>
                    <Button type="button" size="sm" variant="outline" onClick={() => setDeleteId(null)} disabled={submitting}>
                      {copy.reimbursements.settle.cancel}
                    </Button>
                  </div>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="outline" size="lg" onClick={onBack}>
        Kembali ke beranda
      </Button>
    </div>
  )
}

function StatusBadge({ claim, tab }: { claim: Reimbursement; tab: 'outstanding' | 'all' }) {
  if (claim.waived_on) {
    return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.waived}</span>
  }
  // The wire deliberately carries no settled flag (internal/http/
  // reimbursements.go): whether a claim is settled is a fact about the
  // ledger, not the claim row. The outstanding tab knows its rows are all
  // unanswered, so it can say so honestly; the "all" tab keeps the
  // best-effort settled label for history.
  if (tab === 'outstanding') {
    return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.outstanding}</span>
  }
  return <span className="rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">{text.status.settled}</span>
}

function RecordClaimForm({
  members,
  purposes,
  onSubmit,
  onCancel,
  submitting,
}: {
  members: Member[]
  purposes: Purpose[]
  onSubmit: (memberId: number, purposeId: number, amount: number, occurredOn: string, note: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [memberId, setMemberId] = useState<number | null>(null)
  const [purposeId, setPurposeId] = useState<number | null>(null)
  const [amount, setAmount] = useState(0)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')

  // Default purpose to kind:"main" — same as RecordTransaction.tsx.
  useEffect(() => {
    if (purposeId === null) {
      const main = purposes.find((p) => p.kind === 'main')
      if (main) setPurposeId(main.id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [purposes])

  const canSubmit = amount > 0 && memberId !== null && purposeId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || memberId === null || purposeId === null) return
    onSubmit(memberId, purposeId, amount, occurredOn, note.trim())
  }

  return (
    <form className="flex flex-col gap-4 rounded-2xl bg-card p-4 ring-1 ring-foreground/10" onSubmit={handleSubmit} noValidate>
      <h2 className="text-lg font-semibold">{text.record.heading}</h2>

      <MemberPicker
        id="reimburse-member"
        label={text.record.memberLabel}
        members={members}
        value={memberId}
        onChange={setMemberId}
        disabled={submitting}
      />

      <PurposePicker
        id="reimburse-purpose"
        label={text.record.purposeLabel}
        purposes={purposes}
        value={purposeId}
        onChange={setPurposeId}
        disabled={submitting}
      />

      <AmountInput id="reimburse-amount" label={text.record.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="reimburse-date">{text.record.dateLabel}</Label>
        <Input
          id="reimburse-date"
          type="date"
          className="h-11"
          value={occurredOn}
          onChange={(event) => setOccurredOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="reimburse-note">{text.record.noteLabel}</Label>
        <textarea
          id="reimburse-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      <div className="flex gap-2">
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.record.submitting : text.record.submit}
        </Button>
        <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
          {text.record.cancel}
        </Button>
      </div>
    </form>
  )
}

function SettleForm({
  accounts,
  claimAmount,
  onSubmit,
  onCancel,
  submitting,
}: {
  accounts: Account[]
  claimAmount: number
  onSubmit: (accountId: number, occurredOn: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [accountId, setAccountId] = useState<number | null>(null)
  const [occurredOn, setOccurredOn] = useState(todayISODate)

  const canSubmit = accountId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    onSubmit(accountId, occurredOn)
  }

  return (
    <form className="flex flex-col gap-3 rounded-lg bg-muted p-3" onSubmit={handleSubmit} noValidate>
      <h3 className="text-sm font-semibold">{text.settle.heading}</h3>

      <div className="flex items-center justify-between text-sm">
        <span className="text-muted-foreground">{text.record.amountLabel}</span>
        <span className="tabular font-medium">{formatIDR(claimAmount)}</span>
      </div>

      <AccountPicker
        id="settle-account"
        label={text.settle.accountLabel}
        accounts={accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="settle-date">{text.settle.dateLabel}</Label>
        <Input
          id="settle-date"
          type="date"
          className="h-11"
          value={occurredOn}
          onChange={(event) => setOccurredOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={!canSubmit}>
          {submitting ? text.settle.submitting : text.settle.submit}
        </Button>
        <Button type="button" size="sm" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.settle.cancel}
        </Button>
      </div>
    </form>
  )
}

function CorrectForm({
  members,
  purposes,
  claim,
  onSubmit,
  onCancel,
  submitting,
}: {
  members: Member[]
  purposes: Purpose[]
  claim: Reimbursement
  onSubmit: (patch: { member_id?: number; purpose_id?: number; amount?: number; incurred_on?: string; note?: string | null }) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [memberId, setMemberId] = useState(claim.member_id)
  const [purposeId, setPurposeId] = useState(claim.purpose_id)
  const [amount, setAmount] = useState(claim.amount)
  const [occurredOn, setOccurredOn] = useState(claim.incurred_on)
  const [note, setNote] = useState(claim.note ?? '')

  const canSubmit = amount > 0 && memberId !== null && purposeId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return
    onSubmit({
      member_id: memberId,
      purpose_id: purposeId,
      amount,
      incurred_on: occurredOn,
      note: note.trim() === '' ? null : note.trim(),
    })
  }

  return (
    <form className="flex flex-col gap-3 rounded-lg bg-muted p-3" onSubmit={handleSubmit} noValidate>
      <h3 className="text-sm font-semibold">{text.correct.heading}</h3>

      <MemberPicker
        id="correct-member"
        label={text.record.memberLabel}
        members={members}
        value={memberId}
        onChange={setMemberId}
        disabled={submitting}
      />

      <PurposePicker
        id="correct-purpose"
        label={text.record.purposeLabel}
        purposes={purposes}
        value={purposeId}
        onChange={setPurposeId}
        disabled={submitting}
      />

      <AmountInput id="correct-amount" label={text.record.amountLabel} value={amount} onChange={setAmount} disabled={submitting} />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="correct-date">{text.record.dateLabel}</Label>
        <Input
          id="correct-date"
          type="date"
          className="h-11"
          value={occurredOn}
          onChange={(event) => setOccurredOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="correct-note">{text.record.noteLabel}</Label>
        <textarea
          id="correct-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={!canSubmit}>
          {submitting ? text.correct.submitting : text.correct.submit}
        </Button>
        <Button type="button" size="sm" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.correct.cancel}
        </Button>
      </div>
    </form>
  )
}
