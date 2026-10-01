import { useState } from 'react'
import type { ComponentProps } from 'react'
import { Eye, EyeOff } from 'lucide-react'

import { Input } from '@/components/ui/input'
import { copy } from '@/copy/id'
import { cn } from '@/lib/utils'

/**
 * A password field with a peek button (#356): a mistyped password on a phone
 * keyboard is otherwise invisible until the server says no, and enough of
 * those trip the login rate limit.
 *
 * The button is a fixed-label toggle - aria-pressed carries the state, so a
 * screen reader hears "Tampilkan kata sandi, pressed" rather than a label
 * that changes under it. It sits inside the field's right edge at the full
 * 44px, and pr-11 keeps typed text from running under it. preventDefault on
 * pointer down keeps focus in the field, so tapping it on a phone does not
 * close the keyboard mid-word. Ghost and plain ink, like every row control.
 */
export default function PasswordInput({ className, disabled, ...props }: Omit<ComponentProps<'input'>, 'type'>) {
  const [visible, setVisible] = useState(false)

  return (
    <div className="relative">
      <Input {...props} type={visible ? 'text' : 'password'} disabled={disabled} className={cn('pr-11', className)} />
      <button
        type="button"
        aria-label={copy.auth.showPassword}
        aria-pressed={visible}
        disabled={disabled}
        onPointerDown={(event) => event.preventDefault()}
        onClick={() => setVisible((v) => !v)}
        className="absolute inset-y-0 right-0 flex w-11 items-center justify-center rounded-r-lg text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50"
      >
        {visible ? <EyeOff aria-hidden="true" className="size-5" /> : <Eye aria-hidden="true" className="size-5" />}
      </button>
    </div>
  )
}
