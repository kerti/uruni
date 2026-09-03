# Uruni — Design System

**Version 0.1 · 2026-08-08 · Status: draft**

Companion to [`PRD.md`](./PRD.md) and [`Tech-Design.md`](./Tech-Design.md). This is the visual and interaction language the React app builds against. Grounded in the Product Brief's leads: **Plus Jakarta Sans** + the **Sage** palette, soft and rounded, deliberately *not* fintech.

## Design principles

1. **Calm, not corporate.** The emotional core is *tenang* — reconciled means peace of mind. The UI should feel soft, warm, and unhurried.
2. **Simpler than a spreadsheet.** Every screen earns its place; whitespace over density.
3. **Mobile-first, thumb-first.** Big touch targets, primary actions within thumb reach, numeric keypads for money.
4. **Money is legible.** Amounts use tabular figures and clear Rupiah formatting; the balance is the hero.
5. **Not accounting.** No ledgers-as-grids aesthetic, no red/green trading vibes; states are gentle.

## Component library

**Tailwind CSS + shadcn/ui + lucide-react.** Rationale: you own the component code (fits the open-source ethos, no lock-in), theming is pure CSS variables (maps 1:1 to the tokens below), Radix gives accessibility for free, and it's the most training-dense React kit for AI-assisted building. Style shadcn's unstyled primitives with these tokens rather than adopting an opinionated kit (MUI/Chakra/Mantine) whose look fights our warmth.

## Color tokens

Light theme is primary (dark mode deferred). Values are hex for clarity; plug into shadcn's CSS-variable slots.

**Base / neutral**

| Token | Hex | Use |
|---|---|---|
| `--background` | `#F6F4EF` | app background (warm cream) |
| `--card` | `#FFFFFF` | surfaces, cards |
| `--foreground` | `#22323A` | primary text (soft ink) |
| `--muted` | `#EEEBE3` | subtle fills |
| `--muted-foreground` | `#67757C` | secondary text |
| `--border` / `--input` | `#E4E0D7` | hairlines, field borders |
| `--ring` | `#2F6F60` | focus ring |

**Brand**

| Token | Hex | Use |
|---|---|---|
| `--primary` | `#1F5D50` | primary actions, key accents (Forest) |
| `--primary-foreground` | `#FFFFFF` | text on primary |
| `--primary-hover` | `#184C41` | hover/pressed |
| `--accent` | `#7BAE8D` | Sage — tints, badges, illustration accents |
| `--secondary` | `#E7F1EA` | soft sage surface (chips, highlights) |
| `--secondary-foreground` | `#1F5D50` | text on soft sage |

> Contrast note: Sage `#7BAE8D` is too light for white text — use **Forest** as the action color (white text passes AA), and Sage only as tint/accent or with dark text.

**Semantic (deliberately gentle)**

| Token | Hex | Use |
|---|---|---|
| `--success` | `#2E7D5B` | reconciled "cocok"; positive confirmations |
| `--success-soft` | `#E3F1E9` | success backgrounds |
| `--attention` | `#C96C4A` | "selisih" / discrepancy — warm terracotta, **not** alarm-red |
| `--attention-soft` | `#F6E6DD` | attention backgrounds |
| `--destructive` | `#B4453C` | destructive actions only (delete) |
| `--destructive-foreground` | `#FFFFFF` | text on destructive |

The reconciliation states are the emotional heart: **reconciled = success green + a small, satisfying confirmation; discrepancy = warm terracotta, calm and non-punishing** ("Ada selisih Rp 15.000 — cek bareng?").

## Typography

**Plus Jakarta Sans**, self-hosted (bundle the font files — no external CDN call; better reliability and privacy, consistent with the ethos). Weights 400 / 500 / 600 / 700.

| Role | Size | Weight | Notes |
|---|---|---|---|
| Balance / display | 2.25rem (36px) | 700 | `font-variant-numeric: tabular-nums` |
| H1 | 1.5rem (24px) | 600 | screen titles |
| H2 | 1.25rem (20px) | 600 | section headers |
| Body | 1rem (16px) | 400 | min body size on mobile; line-height ~1.5 |
| Small | 0.875rem (14px) | 400 | secondary info |
| Label | 0.8125rem (13px) | 500 | field labels, chips (avoid heavy uppercase) |

All monetary numbers use **tabular-nums** so columns align.

## Shape, elevation, spacing

- **Radius:** `--radius: 0.875rem` (14px). Cards `rounded-2xl`, buttons `rounded-xl`, chips pill.
- **Elevation (soft, warm):** sm `0 1px 2px rgba(34,50,58,.06)` · card `0 2px 8px rgba(34,50,58,.06)` · floating `0 8px 24px rgba(34,50,58,.10)`. No harsh shadows.
- **Spacing scale:** 4 / 8 / 12 / 16 / 24 / 32 / 48 px. Generous padding inside cards (16–20px).

## Controls

Selects are the design system's own control (`components/ui/select.tsx`, Radix `Select` styled to these tokens), never the browser's native `<select>` — the platform list was the one surface in the app that ignored every token on this page. The trigger shows the chosen option's label, passed in by the caller.

## Iconography

**lucide-react**, ~1.75 stroke, rounded caps (lucide default). Fine for functional UI. Per the brand, avoid coins / rupiah symbols / bar charts as decorative/brand imagery — money is shown as *what it became*, not as accounting glyphs.

## Mobile-first interaction

- Minimum touch target **44×44px** — which is `h-11`, the height both `Input` and the select trigger use, so a text field and the picker beside it line up.
- Navigation is a **sticky bottom nav** with five destinations — Beranda · Catat · Iuran · Anggota · Pengaturan — active tab in Forest, the rest muted, `aria-current` alongside the color. Five is the platform's own cap for a tab bar: at 375px that is 75px a tab, which fits an 11px label. A sixth would mean a different pattern, not a narrower tab. The header's fund name is a second way home — never the only one, since a top-corner control is the hardest thing on a phone to reach one-handed. It replaced the circular add-FAB at M6.15: "catat" is a tab now, and no screen has two entry points. Reconcile is deliberately not a tab — the reconciliation banner on home is its affordance.
- Amount inputs use `inputmode="numeric"` and format to `Rp` on blur.
- **Date and month fields are full-width like every other field, plus `appearance-none`.** iOS renders them as a native control whose platform chrome and intrinsic sizing ignore the width rule — that, not the width, is what overran the viewport on a phone. Stripped of it, full width holds the longest case written out in full (*September 2026*; September is the longest Indonesian month name at 9 characters) with room to spare, so no month name is ever abbreviated. The picker that opens is the OS's: its layout and formatting are not reachable from here.
- Home layout: **balance is the hero**, reconciliation status directly beneath, recent activity below.

## Dates

One module formats every date the treasurer reads (`lib/dates.ts`) — it lived as four copies in four screens, which is how one of them came to abbreviate the month while the rate list beside it spelled it out. `dateStyle: 'long'` in `id-ID`, so a date reads **3 September 2026**, never "3 Sep 2026": the voice is unhurried and the rows have the width. A dues period or a rate's effective month reads **September 2026**.

Never hand a bare `'YYYY-MM-DD'` to the `Date` constructor — it parses as UTC midnight and renders a day early west of Greenwich. The helpers split the parts and build a local date.

## Numbers & currency

- Format with `Intl.NumberFormat('id-ID', { style: 'currency', currency: 'IDR', minimumFractionDigits: 0 })` → `Rp 1.450.000` (dot thousands separators, no decimals).
- Store as `int64` rupiah (see Tech-Design ADR-006); format only at the display edge.

## Motion

Calm and quick: 150–250ms ease-out. The reconciled confirmation may use a gentle check + slight scale; nothing bouncy or celebratory-loud. Respect `prefers-reduced-motion`.

## Voice & microcopy

Warm, human, Bahasa Indonesia first (see Positioning). Examples:

- Success: *"Pengeluaran berhasil dicatat."* · *"Kas dan catatan sudah cocok. 🎉"*
- Attention: *"Ada selisih Rp 15.000 — mau dicek bareng?"*
- Dues: *"Masih ada 3 teman yang belum sempat membayar."*

Emoji used *sparingly*, only for warmth on positive states.

## Logo & mark

The mark is the **U-vessel**: a rounded "u" that reads as a small bowl, with a Sage dot nestled inside — a vessel that *pools* contributions (*urun* = to pool), with no money symbolism. It doubles as the lowercase "u" of the wordmark. Wordmark is lowercase **uruni** in Plus Jakarta Sans 700, letter-spacing ≈ −1.

Asset files live in [`/brand`](./brand):

- `uruni-logomark.svg` — primary mark (Forest vessel, Sage dot).
- `uruni-logomark-reversed.svg` — for dark/Forest backgrounds (Cream vessel, Sage dot).
- `uruni-appicon.svg` — Forest rounded-square icon with the knockout vessel.
- `uruni-lockup-horizontal.svg` / `uruni-lockup-stacked.svg` — mark + wordmark.

```svg
<svg viewBox="0 0 64 64" xmlns="http://www.w3.org/2000/svg">
  <path d="M21 18 L21 31 A11 11 0 0 0 43 31 L43 18" fill="none"
        stroke="#1F5D50" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/>
  <circle cx="32" cy="30" r="5" fill="#7BAE8D"/>
</svg>
```

**Usage.** Clear space ≥ the dot's diameter on all sides. Minimum mark size 16px (it stays legible — tested). Colorways: primary on light, reversed on Forest/dark, single-color ink `#22323A` where only one color is available. **Don't** recolor outside the palette, add gradients or shadows, stretch/distort, rotate, or outline the wordmark in a different typeface. For production print/export, convert the wordmark text to outlines (the SVGs currently use live Plus Jakarta Sans text).

## Deferred

Dark mode, full illustration set (line illustrations of "what the money became" — cake, bouquet, lunch), and the emotional-context home view (nice-to-have per PRD §11).
