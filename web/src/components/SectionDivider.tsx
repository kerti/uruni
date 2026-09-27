/**
 * The hairline between a screen's independent sections (#319) - Pengaturan's
 * five settings, Beranda's "now" from its history. Only between sections,
 * never inside one and never inside a form: a line that appears everywhere
 * stops meaning "a new subject starts here" (Design-System.md, "Shape,
 * elevation, spacing"). An <hr> because that is what it is - a thematic
 * break - so a screen reader announces it as a separator.
 */
export default function SectionDivider() {
  return <hr className="border-border" />
}
