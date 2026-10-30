import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/components/toast/ToastProvider'
import { SUCCESS_TOAST_MS, useToast } from '@/components/toast/useToast'

function Buttons() {
  const toast = useToast()
  return (
    <>
      <button onClick={() => toast.success('Sudah disimpan.')}>ok</button>
      <button onClick={() => toast.success('Yang kedua.')}>ok2</button>
      <button onClick={() => toast.attention('Foto belum terunggah.')}>perhatian</button>
    </>
  )
}

function renderToast() {
  return render(
    <ToastProvider>
      <Buttons />
    </ToastProvider>,
  )
}

afterEach(() => {
  vi.useRealTimers()
})

describe('Toast', () => {
  it('keeps one empty live region mounted, and a message lands inside it', async () => {
    renderToast()
    const region = screen.getByRole('status')
    expect(region).toHaveAttribute('aria-live', 'polite')
    expect(region).toHaveAttribute('aria-atomic', 'true')
    expect(region.textContent).toBe('')

    await userEvent.click(screen.getByRole('button', { name: 'ok' }))

    expect(region).toHaveTextContent('Sudah disimpan.')
    expect(screen.getByRole('status')).toBe(region)
  })

  it('hides a success by itself after a few seconds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderToast()
    await userEvent.click(screen.getByRole('button', { name: 'ok' }))
    expect(screen.getByText('Sudah disimpan.')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(SUCCESS_TOAST_MS - 100)
    })
    expect(screen.getByText('Sudah disimpan.')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(200)
    })
    expect(screen.queryByText('Sudah disimpan.')).not.toBeInTheDocument()
    expect(screen.getByRole('status').textContent).toBe('')
  })

  it('dismisses on a tap', async () => {
    renderToast()
    await userEvent.click(screen.getByRole('button', { name: 'ok' }))
    await userEvent.click(screen.getByRole('button', { name: 'Sudah disimpan.' }))
    expect(screen.queryByText('Sudah disimpan.')).not.toBeInTheDocument()
  })

  it('keeps an attention toast until it is tapped', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderToast()
    await userEvent.click(screen.getByRole('button', { name: 'perhatian' }))

    act(() => {
      vi.advanceTimersByTime(SUCCESS_TOAST_MS * 5)
    })
    expect(screen.getByText('Foto belum terunggah.')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Foto belum terunggah.' }))
    expect(screen.queryByText('Foto belum terunggah.')).not.toBeInTheDocument()
  })

  it('shows one toast at a time: a new one replaces the current, and restarts the clock', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderToast()
    await userEvent.click(screen.getByRole('button', { name: 'perhatian' }))
    await userEvent.click(screen.getByRole('button', { name: 'ok' }))
    expect(screen.queryByText('Foto belum terunggah.')).not.toBeInTheDocument()
    expect(screen.getByText('Sudah disimpan.')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(SUCCESS_TOAST_MS - 500)
    })
    await userEvent.click(screen.getByRole('button', { name: 'ok2' }))
    expect(screen.queryByText('Sudah disimpan.')).not.toBeInTheDocument()

    // The first one's clock must not cut the second short.
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(screen.getByText('Yang kedua.')).toBeInTheDocument()

    act(() => {
      vi.advanceTimersByTime(SUCCESS_TOAST_MS)
    })
    expect(screen.queryByText('Yang kedua.')).not.toBeInTheDocument()
  })
})
