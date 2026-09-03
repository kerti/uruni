import '@testing-library/jest-dom/vitest'

// Radix's Select (components/ui/select.tsx) drives its popup with pointer
// capture, ResizeObserver and scrollIntoView - three browser APIs jsdom does
// not implement at all. Without these stubs every test that opens a select
// dies inside Radix rather than in the code under test. Nothing here changes
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
