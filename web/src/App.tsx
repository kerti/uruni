import { useState } from 'react'
import { CircleCheck, TriangleAlert } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { copy } from '@/copy/id'

type Health = 'idle' | 'checking' | 'online' | 'offline'

/**
 * The M1 smoke page: proof that one binary serves this bundle and answers
 * /healthz on the same origin (ADR-001). The everyday UI is M6 — nothing here
 * is meant to survive it.
 */
export default function App() {
  const [health, setHealth] = useState<Health>('idle')

  async function check() {
    setHealth('checking')
    try {
      const res = await fetch('/healthz')
      setHealth(res.ok ? 'online' : 'offline')
    } catch {
      setHealth('offline')
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <img src="/favicon.svg" alt="" className="size-12" />
          <CardTitle className="text-2xl font-semibold">{copy.smoke.heading}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-muted-foreground">{copy.smoke.body}</p>
          <Button size="lg" onClick={check} disabled={health === 'checking'}>
            {health === 'checking' ? copy.smoke.checking : copy.smoke.check}
          </Button>
          {health === 'online' && (
            <p className="flex items-center gap-2 text-success">
              <CircleCheck aria-hidden="true" />
              {copy.smoke.online}
            </p>
          )}
          {health === 'offline' && (
            <p className="flex items-center gap-2 text-attention">
              <TriangleAlert aria-hidden="true" />
              {copy.smoke.offline}
            </p>
          )}
        </CardContent>
      </Card>
    </main>
  )
}
