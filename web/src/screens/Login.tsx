import { useState } from 'react'
import type { FormEvent } from 'react'
import { WarningCircle } from '@phosphor-icons/react'

import AuthChrome from '@/components/AuthChrome'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import PasswordInput from '@/components/PasswordInput'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { login } from '@/lib/auth'
import { useApi } from '@/lib/useApi'
import { errorCode } from '@/lib/api'
import { copy } from '@/copy/id'
import type { AuthUser, DemoLogin } from '@/lib/auth'

const text = copy.auth.login

/**
 * The everyday door (POST /api/login). Rendered by App.tsx when
 * has_account: true and authenticated: false.
 *
 * Two failure codes get their own copy rather than the shared ErrorState:
 * invalid_credentials must read as an ordinary "check what you typed", and
 * too_many_requests as a temporary lockout - folding them together would hide
 * *why* the door isn't opening. Every other code (network_error,
 * internal_error, ...) goes through the shared ErrorState.
 *
 * `demo` is the public demo's shared login (ADR-045), which the session probe
 * carries only under URUNI_DEMO: the form starts filled in, and a notice above
 * it spells the credentials out for a visitor who clears the fields. Null, the
 * default, is every real instance: an empty form and no notice.
 */
export default function Login({ onLoggedIn, demo = null }: { onLoggedIn: (user: AuthUser) => void; demo?: DemoLogin | null }) {
  const [email, setEmail] = useState(demo?.email ?? '')
  const [password, setPassword] = useState(demo?.password ?? '')
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

  const code = state.status === 'error' ? errorCode(state.error) : undefined

  return (
    <AuthChrome>
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <CardTitle className="text-xl font-semibold">{text.heading}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {demo && <DemoNotice demo={demo} />}
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
              <PasswordInput
                id="login-password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </div>
            {code === 'invalid_credentials' && (
              <p role="alert" className="flex items-center gap-2 text-attention-text">
                <WarningCircle aria-hidden="true" />
                {text.invalidCredentials}
              </p>
            )}
            {code === 'too_many_requests' && (
              <p role="alert" className="flex items-center gap-2 text-attention-text">
                <WarningCircle aria-hidden="true" />
                {text.tooManyRequests}
              </p>
            )}
            {state.status === 'error' && state.error && code !== 'invalid_credentials' && code !== 'too_many_requests' && (
              <ErrorState error={state.error} />
            )}
            <Button type="submit" size="lg" disabled={submitting}>
              {submitting ? text.submitting : text.submit}
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthChrome>
  )
}

/** The demo's notice (ADR-045): what this is, then the credentials in full. */
function DemoNotice({ demo }: { demo: DemoLogin }) {
  const demoText = text.demo
  return (
    <div role="note" className="flex flex-col gap-2 rounded-lg bg-success-soft px-3 py-2 text-sm text-foreground">
      <p>
        <strong className="font-semibold">{demoText.title}</strong>
      </p>
      <p>{demoText.body}</p>
      <p>
        {demoText.emailLabel} <code className="font-mono font-medium whitespace-nowrap">{demo.email}</code>
        <br />
        {demoText.passwordLabel} <code className="font-mono font-medium whitespace-nowrap">{demo.password}</code>
      </p>
    </div>
  )
}
