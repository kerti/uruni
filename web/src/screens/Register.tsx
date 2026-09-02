import { useState } from 'react'
import type { FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { register } from '@/lib/auth'
import { useApi } from '@/lib/useApi'
import { copy } from '@/copy/id'
import type { AuthUser } from '@/lib/auth'

const text = copy.auth.register

// The same floor internal/auth/auth.go's MinPasswordLength enforces, named
// here rather than inline so the two are greppable as a pair. The server is
// the real guard; this only saves the treasurer a round trip.
const MIN_PASSWORD_LENGTH = 8

/**
 * The one-shot bootstrap account screen (ADR-030 decision 2). Rendered by
 * App.tsx only while GET /api/session answers has_account: false - once a
 * registration succeeds there is no route back here in this same session,
 * so a 409 (a second registration racing this one) is the only server error
 * this screen has to plan for, and it renders as an ordinary ErrorState
 * rather than anything special-cased.
 *
 * A successful register also establishes the session server-side
 * (register.go's own comment), so onRegistered here is what moves App past
 * auth entirely - never to a login screen the treasurer never asked for.
 */
export default function Register({ onRegistered }: { onRegistered: (user: AuthUser) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [tooShort, setTooShort] = useState(false)
  const [state, run] = useApi<AuthUser>()

  const submitting = state.status === 'loading'

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    // Checked before the request ever leaves the browser - the treasurer is
    // told about the 8-character floor without paying a round trip for it.
    // auth.go enforces the same floor server-side; this is a courtesy, not
    // the only guard.
    if (password.length < MIN_PASSWORD_LENGTH) {
      setTooShort(true)
      return
    }
    setTooShort(false)

    void run(async () => {
      const user = await register(email, password)
      onRegistered(user)
      return user
    })
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <CardTitle className="text-2xl font-semibold">{text.heading}</CardTitle>
          <p className="text-muted-foreground">{text.body}</p>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="register-email">{text.emailLabel}</Label>
              <Input
                id="register-email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="register-password">{text.passwordLabel}</Label>
              <Input
                id="register-password"
                type="password"
                autoComplete="new-password"
                required
                value={password}
                onChange={(event) => {
                  setPassword(event.target.value)
                  setTooShort(false)
                }}
              />
            </div>
            {tooShort && (
              <p role="alert" className="text-attention">
                {text.passwordTooShort}
              </p>
            )}
            {state.status === 'error' && state.error && <ErrorState error={state.error} />}
            <Button type="submit" size="lg" disabled={submitting}>
              {submitting ? text.submitting : text.submit}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
