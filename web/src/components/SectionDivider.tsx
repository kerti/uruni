/**
 * The hairline between a screen's independent sections - Pengaturan's five
 * settings, Beranda's "now" from its history. Only between sections, never
 * inside one or a form: a line that appears everywhere stops meaning "a new
 * subject starts here" (Design-System.md, "Shape, elevation, spacing"). An
 * <hr>, a thematic break, so a screen reader announces a separator.
 */
export default function SectionDivider() {
  return <hr className="border-border" />
}
