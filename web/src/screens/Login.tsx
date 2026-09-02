import { useState } from 'react'
import type { FormEvent } from 'react'
import { CircleAlert } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { login } from '@/lib/auth'
import { useApi } from '@/lib/useApi'
import { copy } from '@/copy/id'
import type { AuthUser } from '@/lib/auth'

const text = copy.auth.login

/**
 * The everyday door (POST /api/login). Rendered by App.tsx when
 * has_account: true and authenticated: false.
 *
 * Two of login's failure codes get their own copy rather than falling
 * through to the shared ErrorState (issue #115): invalid_credentials must
 * read as an ordinary "check what you typed", and too_many_requests must
 * read as a temporary lockout, not the same generic sentence - folding
 * them together would hide from the treasurer *why* the door isn't
 * opening. Every other code (network_error, internal_error, ...) still
 * goes through the shared ErrorState so this screen doesn't have to
 * duplicate that mapping.
 */
export default function Login({ onLoggedIn }: { onLoggedIn: (user: AuthUser) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [state, run] = useApi<AuthUser>()

  const submitting = state.status === 'loading'

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void run(async () => {
      const user = await login(email, password)
      onLoggedIn(user)
      return user
    })
  }

  const code = state.status === 'error' ? state.error?.code : undefined

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
              <Label htmlFor="login-email">{text.emailLabel}</Label>
              <Input
                id="login-email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="login-password">{text.passwordLabel}</Label>
              <Input
                id="login-password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </div>
            {code === 'invalid_credentials' && (
              <p role="alert" className="flex items-center gap-2 text-attention">
                <CircleAlert aria-hidden="true" />
                {text.invalidCredentials}
              </p>
            )}
            {code === 'too_many_requests' && (
              <p role="alert" className="flex items-center gap-2 text-attention">
                <CircleAlert aria-hidden="true" />
                {text.tooManyRequests}
              </p>
            )}
            {state.status === 'error' &&
              state.error &&
              code !== 'invalid_credentials' &&
              code !== 'too_many_requests' && <ErrorState error={state.error} />}
            <Button type="submit" size="lg" disabled={submitting}>
              {submitting ? text.submitting : text.submit}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
