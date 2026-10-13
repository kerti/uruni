/** A failed write's reason, kept above the form so she can read it before
 * deciding again. */
export function FormAlert({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <p role="alert" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
      {message}
    </p>
  )
}
