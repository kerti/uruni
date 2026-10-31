import '@testing-library/jest-dom/vitest'
import { createElement, forwardRef, type ComponentType } from 'react'
import { vi } from 'vitest'

// Phosphor icons carry no class naming them, so a test could not tell a
// row's glyph from another. Each real icon is wrapped to add
// data-icon="<Name>" - the same SVG renders, and a test asks
// `[data-icon="ArrowUUpLeft"]`.
vi.mock('@phosphor-icons/react', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  const wrapped: Record<string, unknown> = {}
  for (const [name, value] of Object.entries(actual)) {
    const isIcon =
      /^[A-Z]/.test(name) &&
      name !== 'IconContext' &&
      name !== 'IconBase' &&
      typeof value === 'object' &&
      value !== null &&
      'render' in value
    wrapped[name] = isIcon
      ? forwardRef<SVGSVGElement, Record<string, unknown>>((props, ref) =>
          createElement(value as ComponentType<Record<string, unknown>>, { ...props, ref, 'data-icon': name }),
        )
      : value
  }
  return wrapped
})

// Radix's Select (components/ui/select.tsx) drives its popup with pointer
// capture, ResizeObserver and scrollIntoView - three browser APIs jsdom does
// not implement. Without these stubs every test that opens a select dies
// inside Radix rather than in the code under test. Nothing here changes
// behaviour; they are the missing platform, not test doubles.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}
if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = () => {}
}
if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => {}
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}
if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
