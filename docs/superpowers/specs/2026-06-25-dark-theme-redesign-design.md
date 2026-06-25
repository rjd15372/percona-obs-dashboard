# Dark Theme Redesign — Design

**Date:** 2026-06-25
**Status:** Approved (design)

## Problem

The current dark theme has three issues the user dislikes:

1. **Purple/violet tint in the backgrounds** — the base (`#0B0912`) and cards (`#181426`) carry a noticeable purple cast, and even the text is lavender (`#EDE8FD`).
2. **Accent colors** — the status/brand accents are too saturated/neon (e.g. brand `#4A9EFF`, fail `#FF6166`, broken `#FF5E7A`).
3. **Too dark overall** — the base is nearly pure black, making the UI feel heavy.

## Goal

Replace the dark palette with a **neutral cool "slate" base**, lifted a notch out of the near-black range, paired with **cool-harmonized accent colors** (teal/cyan family) that are clear but not neon. Light theme is unchanged.

## Scope

- **In scope:** The 24 CSS custom properties inside the `[data-theme="dark"]` block of `frontend/src/assets/theme.css`. This is a pure value swap — no structural, markup, or component changes.
- **Out of scope:** The light theme (`:root`), the Tailwind config token mapping, the theme-toggle mechanism, component markup, and the font stack. None of these change.

## Architecture

The theming system is already well-structured for this change: all colors are CSS custom properties defined per-theme in one file (`frontend/src/assets/theme.css`), mapped into Tailwind via `tailwind.config.ts`, and consumed everywhere as `var(--token)`. Components and composables (e.g. `useEventDisplay.ts`'s `GLYPH_COLOR`, `GLYPH_BG`, `TAG_STYLE`) reference the variables, not raw hex. **Changing the values in the dark block updates the entire dark UI with no other edits.**

## The New Dark Palette

The change is confined to the `[data-theme="dark"]` block. Full before → after:

| Token | Old (purple/neon) | New (slate/cool) |
|---|---|---|
| `--bg-app` | `#0B0912` | `#11161D` |
| `--bg-card` | `#181426` | `#1A222E` |
| `--bg-card-2` | `#13101F` | `#151B24` |
| `--bg-muted` | `#1A1626` | `#1E2733` |
| `--brand-purple` | `#4A9EFF` | `#3FAFCB` |
| `--brand-purple-tint` | `rgba(74,158,255,0.15)` | `rgba(63,175,203,0.16)` |
| `--ok` | `#42C97E` | `#2DBE96` |
| `--ok-tint` | `#0D3320` | `rgba(45,190,150,0.16)` |
| `--fail` | `#FF6166` | `#E15F78` |
| `--fail-tint` | `#3D1215` | `rgba(225,95,120,0.16)` |
| `--warn` | `#F0A52A` | `#DCB446` |
| `--warn-tint` | `#3A2800` | `rgba(220,180,70,0.16)` |
| `--broken` | `#FF5E7A` | `#C84A6A` |
| `--broken-tint` | `#3D0E1A` | `rgba(200,74,106,0.16)` |
| `--blocked` | `#9C97A8` | `#8595A8` |
| `--blocked-tint` | `#1E1A2A` | `rgba(133,149,168,0.16)` |
| `--info` | `#5BA0F0` | `#5B93C9` |
| `--info-tint` | `#0D1E3D` | `rgba(91,147,201,0.16)` |
| `--text-primary` | `#EDE8FD` | `#E8EDF4` |
| `--text-secondary` | `#9C97A8` | `#94A2B5` |
| `--text-muted` | `#6B6680` | `#6B7888` |
| `--border` | `rgba(255,255,255,0.08)` | `rgba(255,255,255,0.09)` |
| `--border-strong` | `rgba(255,255,255,0.14)` | `rgba(255,255,255,0.15)` |
| `--tech-postgres` | `#4A9EFF` | `#3FAFCB` |
| `--tint-postgres` | `rgba(74,158,255,0.15)` | `rgba(63,175,203,0.16)` |

### Design rationale

- **Slate base.** Backgrounds shift from purple-black to a cool blue-gray (`#11161D` app, `#1A222E` cards), lifted off pure black so the UI feels lighter. The text loses its lavender cast and becomes a clean cool off-white (`#E8EDF4`).
- **Cool-harmonized accents.** Status colors are shifted toward the teal/cyan family to harmonize with the slate base and toned down from neon: ok → teal-green, warn → gold, fail → cool rose-red, broken → deeper rose, info → steel blue.
- **Brand vs. info separation.** Brand becomes a distinctive cyan-teal (`#3FAFCB`) while info stays a calmer steel blue (`#5B93C9`), so the two bluish accents remain visually distinct. `--tech-postgres` mirrors brand, as it does today.
- **Tints become translucent.** Dark `*-tint` values change from solid dark hex to low-alpha (`0.16`) rgba of their accent. This keeps chip/glyph backgrounds consistent with the accent hue and renders correctly over any dark surface. (The light theme keeps its solid-hex tints; tints are defined per-theme so this divergence is fine.)

## Data Flow

No change. `App.vue` toggles `data-theme="dark"` on `<html>`; CSS variables resolve per-theme; Tailwind utilities and inline `var(--token)` references pick up the new values automatically.

## Error Handling

Not applicable — this is a static CSS value change with no runtime logic.

## Testing / Verification

Visual verification only:

1. Run the frontend dev server.
2. Toggle to dark mode via the header button.
3. Confirm on the dashboard: backgrounds are neutral slate (no purple), the UI feels lighter than before, status glyphs/chips (succeeded/warning/failed/started) and tags render with the new cool accents, and text is readable cool off-white.
4. Confirm light mode is visually unchanged.

## Acceptance Criteria

- [ ] All 24 variables in the `[data-theme="dark"]` block match the "New" column above.
- [ ] The `:root` (light) block is byte-for-byte unchanged.
- [ ] No purple/violet cast remains in dark-mode backgrounds or text.
- [ ] Dark mode renders correctly with no broken/unreadable colors (status chips, tags, event glyphs, borders).
