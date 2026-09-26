import { useEffect, useRef, useState } from 'react'
import { Camera, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'

const text = copy.receipts

/** The three types the backend accepts (internal/http's receipt routes) -
 * HEIC is deliberately absent, so a phone's default camera format is
 * refused by the OS picker itself on platforms that honour `accept`,
 * rather than only after an upload round-trip. No `capture` attribute: the
 * issue is explicit that skipping the photo is the normal path, and forcing
 * the camera would take away the "pick an existing photo" option every
 * phone's file picker already offers beside it. */
const ACCEPTED_TYPES = 'image/jpeg,image/png,image/webp'

/**
 * A reusable optional-photo field (#154): `<input type="file">` plus a
 * small preview of the chosen file, with a way to clear it before
 * submitting. Used at record time (RecordTransaction.tsx, the
 * reimbursement claim form) and, wrapped by ReceiptDialog.tsx, for
 * attaching or replacing a photo after the fact.
 *
 * Controlled by `value`/`onChange` alone - this component holds no upload
 * state of its own, since at record time the file travels with the rest of
 * the form and is only posted after the parent row exists.
 */
export default function ReceiptPicker({
  id,
  value,
  onChange,
  disabled,
}: {
  id: string
  value: File | null
  onChange: (file: File | null) => void
  disabled?: boolean
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)

  // The preview is an object URL over the file already sitting in memory -
  // never uploaded just to be shown back. Revoked on every change and on
  // unmount, so a form opened and abandoned repeatedly does not leak one
  // per photo picked.
  useEffect(() => {
    if (!value) {
      setPreviewUrl(null)
      return
    }
    const url = URL.createObjectURL(value)
    setPreviewUrl(url)
    return () => URL.revokeObjectURL(url)
  }, [value])

  function handlePick(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0] ?? null
    onChange(file)
    // Clears the input's own value so picking the exact same file again
    // after clearing still fires a change event.
    event.target.value = ''
  }

  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {!value && (
        <Button
          type="button"
          variant="outline"
          size="lg"
          className="justify-start"
          disabled={disabled}
          onClick={() => inputRef.current?.click()}
        >
          <Camera aria-hidden="true" />
          {text.addFromRow}
        </Button>
      )}

      {value && (
        <div className="flex items-center gap-3 rounded-lg bg-muted p-2">
          {previewUrl && (
            // Decorative beside the file name text right after it - the
            // pair together are the accessible description, same split
            // TransactionList.tsx's own icons use.
            <img src={previewUrl} alt="" className="size-14 shrink-0 rounded-md object-cover" />
          )}
          <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">{value.name}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-11 shrink-0"
            aria-label={text.clearSelected}
            disabled={disabled}
            onClick={() => onChange(null)}
          >
            <X aria-hidden="true" />
          </Button>
        </div>
      )}

      <input
        ref={inputRef}
        id={id}
        type="file"
        accept={ACCEPTED_TYPES}
        // The button above is the visible control; the input only carries
        // the name for assistive tech and tests, and stays out of the tab
        // order so keyboard users meet one control, not two.
        aria-label={text.addFromRow}
        tabIndex={-1}
        className="sr-only"
        disabled={disabled}
        onChange={handlePick}
      />
    </div>
  )
}
