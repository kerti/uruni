import { useEffect, useState, type FormEvent } from 'react'
import { CircleAlert, CircleCheck } from 'lucide-react'

import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import PasswordInput from '@/components/PasswordInput'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { changeEmail, changePassword, getUser } from '@/lib/auth'
import { useDemoMode } from '@/lib/demoMode'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { AuthUser } from '@/lib/auth'

const text = copy.settings.user

// The floor internal/auth/auth.go's MinPasswordLength enforces, as Register.tsx
// names it: the server is the real guard, this only saves a round trip.
const MIN_PASSWORD_LENGTH = 8

/**
 * The Akun bendahara card (#386): who is signed in, and the two edits to that
 * one login. The instance still has a single account (ADR-030); this changes
 * it and adds no second one.
 *
 * Each edit is a dialog addressed by `?edit=user:email` and
 * `?edit=user:password` like every other Pengaturan dialog (ADR-032), and
 * asks for the current password: a borrowed, unlocked phone should not be able
 * to take the account over. Either one signs every other device out and keeps
 * this one.
 *
 * Pengaturan's other saves close their dialog and let the card show the new
 * value. The email does that here too; a password has nothing to show, so a
 * saved edit also leaves a success line under the card, in the green
 * App.tsx's recorded-a-transaction line uses.
 */
export default function User() {
  const [loadState, loadRun] = useApi<AuthUser>()
  const [notice, setNotice] = useState<string | null>(null)
  const { value, open, close, clear } = useDialogParam()

  useEffect(() => {
    void loadRun(getUser)
  }, [loadRun])

  const user = loadState.data ?? null

  // 'user' is this section's prefix (dialogTarget.ts); `email` and
  // `password` are its two values, which the parser reads as `invalid`, so
  // they are told apart by the raw string, as FundName does. Any other
  // `user:` value is cleared once; a sibling's is never touched.
  const target = parseDialogTarget('user', value)
  const isOwn = target.kind !== 'foreign'
  const editingEmail = value === 'user:email'
  const editingPassword = value === 'user:password'

  useEffect(() => {
    if (!isOwn || editingEmail || editingPassword) return
    clear()
  }, [value, isOwn, editingEmail, editingPassword, clear])

  function openDialog(next: 'user:email' | 'user:password') {
    setNotice(null)
    open(next)
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        {user && <p className="text-sm break-all text-muted-foreground">{text.body(user.email)}</p>}
      </div>

      {loadState.status === 'idle' || loadState.status === 'loading' ? (
        <Loading />
      ) : loadState.status === 'error' || user === null ? (
        loadState.error && <ErrorState error={loadState.error} onRetry={() => void loadRun(getUser)} />
      ) : (
        <div className="flex gap-3">
          <Button type="button" variant="outline" size="lg" className="flex-1 justify-center" onClick={() => openDialog('user:email')}>
            {text.changeEmail}
          </Button>
          <Button type="button" variant="outline" size="lg" className="flex-1 justify-center" onClick={() => openDialog('user:password')}>
            {text.changePassword}
          </Button>
        </div>
      )}

      {notice && (
        <p role="status" className="flex items-center gap-2 text-success">
          <CircleCheck aria-hidden="true" />
          {notice}
        </p>
      )}

      <Dialog
        open={editingEmail && user !== null}
        onOpenChange={(next) => {
          if (!next) close()
        }}
      >
        <DialogContent closeLabel={copy.common.close}>
          <DialogHeader>
            <DialogTitle>{text.email.title}</DialogTitle>
          </DialogHeader>
          <EmailForm
            onChanged={(updated) => {
              void loadRun(() => Promise.resolve(updated), { silent: true })
              setNotice(text.email.success)
              close()
            }}
          />
        </DialogContent>
      </Dialog>

      <Dialog
        open={editingPassword && user !== null}
        onOpenChange={(next) => {
          if (!next) close()
        }}
      >
        <DialogContent closeLabel={copy.common.close}>
          <DialogHeader>
            <DialogTitle>{text.password.title}</DialogTitle>
          </DialogHeader>
          <PasswordForm
            onChanged={() => {
              setNotice(text.password.success)
              close()
            }}
          />
        </DialogContent>
      </Dialog>
    </section>
  )
}

// The forms live inside DialogContent, which Radix mounts only while the
// dialog is open: every opening starts from empty fields and no stale error,
// with nothing to reset by hand.

function EmailForm({ onChanged }: { onChanged: (user: AuthUser) => void }) {
  const [state, run] = useApi<AuthUser>()
  const [email, setEmail] = useState('')
  const [current, setCurrent] = useState('')
  const demo = useDemoMode()

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (demo || email.trim() === '' || current === '') return
    void run(async () => {
      const updated = await changeEmail(email, current)
      onChanged(updated)
      return updated
    })
  }

  return (
    <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="user-email-new">{text.email.newLabel}</Label>
        <Input
          id="user-email-new"
          type="email"
          autoComplete="email"
          required
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="user-email-current">{text.email.currentLabel}</Label>
        <PasswordInput
          id="user-email-current"
          autoComplete="current-password"
          required
          value={current}
          onChange={(event) => setCurrent(event.target.value)}
        />
      </div>
      <p className="text-sm text-muted-foreground">{text.email.note}</p>
      {demo && <p className="text-sm text-attention-text">{text.email.demoLocked}</p>}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <DialogFooter className="mt-1">
        <Button type="submit" size="lg" disabled={demo || busy || email.trim() === '' || current === ''}>
          {busy ? text.email.saving : text.email.save}
        </Button>
      </DialogFooter>
    </form>
  )
}

function PasswordForm({ onChanged }: { onChanged: () => void }) {
  const [state, run] = useApi<void>()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [localError, setLocalError] = useState<'short' | 'mismatch' | null>(null)
  const demo = useDemoMode()

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (demo || current === '' || next === '' || repeat === '') return

    // Checked before the request leaves the browser: neither needs the server,
    // and a wrong guess at the current password is rate limited, so a typo in
    // the new one should not cost an attempt. The server enforces the length
    // itself; it never sees the repeat.
    if (next.length < MIN_PASSWORD_LENGTH) {
      setLocalError('short')
      return
    }
    if (next !== repeat) {
      setLocalError('mismatch')
      return
    }
    setLocalError(null)

    void run(async () => {
      await changePassword(next, current)
      onChanged()
    })
  }

  return (
    <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="user-password-current">{text.password.currentLabel}</Label>
        <PasswordInput
          id="user-password-current"
          autoComplete="current-password"
          required
          value={current}
          onChange={(event) => setCurrent(event.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="user-password-new">{text.password.newLabel}</Label>
        <PasswordInput
          id="user-password-new"
          autoComplete="new-password"
          required
          value={next}
          onChange={(event) => {
            setNext(event.target.value)
            setLocalError(null)
          }}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="user-password-repeat">{text.password.repeatLabel}</Label>
        <PasswordInput
          id="user-password-repeat"
          autoComplete="new-password"
          required
          value={repeat}
          onChange={(event) => {
            setRepeat(event.target.value)
            setLocalError(null)
          }}
        />
      </div>
      <p className="text-sm text-muted-foreground">{text.password.note}</p>
      {demo && <p className="text-sm text-attention-text">{text.password.demoLocked}</p>}
      {localError && (
        <p role="alert" className="flex items-center gap-2 text-attention-text">
          <CircleAlert aria-hidden="true" />
          {localError === 'short' ? copy.auth.register.passwordTooShort : text.password.mismatch}
        </p>
      )}
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
      <DialogFooter className="mt-1">
        <Button type="submit" size="lg" disabled={demo || busy || current === '' || next === '' || repeat === ''}>
          {busy ? text.password.saving : text.password.save}
        </Button>
      </DialogFooter>
    </form>
  )
}
