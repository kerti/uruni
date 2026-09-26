import { Dialog as DialogPrimitive } from 'radix-ui'
import { X } from 'lucide-react'
import { useLayoutEffect, useRef } from 'react'

import { copy } from '@/copy/id'

/**
 * The full-screen zoom view opened by tapping a photo in ReceiptDialog
 * (#154 follow-up) - a second, black, safe-area-aware Dialog layered above
 * it, so it needs its own primitive rather than ui/dialog.tsx's centred card.
 *
 * One state only: the photo at its stored pixel size (receipts are
 * re-encoded to at most 1600px on the long edge - internal/http's
 * processReceiptImage) inside an `overflow-auto` box, panned with the
 * platform's own scrolling. The dialog's full-width photo is already the
 * "whole photo" view, so a full-screen fit mode would only repeat it. No
 * zoom/gesture library and no pinch handling - this is the whole of "zoom".
 *
 * Opening scrolls so the point tapped in the dialog (`anchor`, as fractions
 * of the image) lands at the centre of the screen, rather than snapping to
 * the top-left corner. A tap on the photo, the close button or Escape all go
 * back to the dialog.
 *
 * No swipe-down-to-close: a downward pan meant to dismiss cannot be told
 * apart from one meant to move around the photo.
 */
export default function ReceiptViewer({
  open,
  src,
  anchor,
  onClose,
}: {
  open: boolean
  /** GET /api/receipts/{id}'s URL (lib/receipts.ts's receiptUrl), or null
   * while no photo is the current target - mirrors ReceiptDialog's own
   * `parentId` idiom so this component never has to know about receipt ids
   * itself. */
  src: string | null
  /** Where the photo was tapped, as fractions (0..1) across and down it. */
  anchor: { fx: number; fy: number } | null
  onClose: () => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)

  // Centres the tapped point. Runs once the dialog has mounted and again on
  // the image's load event, since naturalWidth is 0 until the image decodes
  // (usually it is already cached from the dialog, and the first run wins).
  function centreOnAnchor() {
    const container = containerRef.current
    const img = imgRef.current
    if (!container || !img || img.naturalWidth === 0) return
    const { fx, fy } = anchor ?? { fx: 0.5, fy: 0.5 }
    container.scrollLeft = fx * img.naturalWidth - container.clientWidth / 2
    container.scrollTop = fy * img.naturalHeight - container.clientHeight / 2
  }

  useLayoutEffect(() => {
    if (open) centreOnAnchor()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-centre per open/photo only
  }, [open, src])

  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-[70] bg-black data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed inset-0 z-[70] flex flex-col bg-black outline-none data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out"
        >
          <DialogPrimitive.Title className="sr-only">{copy.receipts.dialogHeading}</DialogPrimitive.Title>
          {src && (
            // The image's m-auto centres a photo smaller than the screen
            // and still lets one that is larger scroll from its edge.
            <div ref={containerRef} className="flex flex-1 overflow-auto">
              <img
                ref={imgRef}
                src={src}
                alt=""
                onLoad={centreOnAnchor}
                onClick={onClose}
                className="m-auto block max-w-none shrink-0 cursor-zoom-out"
              />
            </div>
          )}

          <DialogPrimitive.Close
            aria-label={copy.common.close}
            className="fixed top-[max(0.5rem,env(safe-area-inset-top))] right-[max(0.5rem,env(safe-area-inset-right))] z-[71] flex size-11 items-center justify-center rounded-full bg-black/50 text-white outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <X aria-hidden="true" />
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
