import { useState } from 'react'
import { CircleCheck, TriangleAlert } from 'lucide-react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import OfflineBanner from '@/components/states/OfflineBanner'
import { copy } from '@/copy/id'

/**
 * Router root for the whole SPA (M6.2). This shell has a known death date:
 * M6.6 replaces it with the everyday-loop layout, and every screen below is
 * itself a placeholder later M6 slices replace one at a time. It exists now
 * so `/healthz` keeps resolving through a real router during review, before
 * any of that lands.
 */
export default function App() {
  return (
    <BrowserRouter>
      <OfflineBanner />
      <Routes>
        <Route path="*" element={<SmokePage />} />
      </Routes>
    </BrowserRouter>
  )
}

type Health = 'idle' | 'checking' | 'online' | 'offline'

/**
 * The M1 smoke page, carried over unpolished as a connectivity check a human
 * can still run by hand. Nothing here is meant to survive the screens later
 * M6 slices add in its place.
 */
function SmokePage() {
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
