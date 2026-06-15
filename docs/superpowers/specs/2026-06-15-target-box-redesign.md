# Target Box Redesign

## Context

Each package card shows a list of build targets (repo/arch pairs). Currently, target detail — build reason, blocked_by, finished outcome — is rendered as flat inline spans with no consistent visual structure. With five distinct states now carrying state-specific detail, the flat layout is unreadable.

All state-specific data is already available from the backend:
- `Target.details` — populated from `_result?view=status` for `finished`, `unresolvable`, and `broken` states
- `Target.blocked_by` — from `_result?view=status` for `blocked` targets
- `Target.build_reason` / `Target.build_reason_packages` — from `_reason` API

## Design

### Target row structure

Each target is a collapsible box. The header row always shows:

```
[dot] [repo/distro_arch]   [state label]  [log ↗]  [▸]
```

- **dot**: coloured square (8×8px, border-radius 2px) matching state colour
- **repo/distro_arch**: `{repo}/{arch}` — e.g. `openSUSE_Tumbleweed/x86_64`
- **state label**: right-aligned, coloured, bold — e.g. `blocked`, `building`
- **log ↗**: purple link, always present (links to OBS build log)
- **▸/▾ chevron**: shown only when the target has content to display in its body; hidden otherwise

Clicking anywhere on the header toggles the body open/closed. Clicking `log ↗` does not toggle (it navigates).

Per-target expand state is tracked in a `Set<string>` keyed by `repo/arch`. This is independent of the existing `showAll` toggle (which controls the "show N more targets" cut-off).

### Finished state: title colouring

For `finished` targets, the dot and state label change colour based on `target.details`:

| `details` value | Dot and label colour |
|---|---|
| `succeeded` | green (`#9ece6a`) |
| `unchanged` | green (`#9ece6a`) |
| `failed` | red (`#f44747`) |
| *(empty)* | amber (`#e0af68`) — no chevron |

The card background tint follows the same logic (green-tinted for success, red-tinted for failed, amber for unknown).

### Body content

The body is shown when the target is expanded. It contains up to two labelled sections separated by a thin divider:

**1. Build reason** (shown when `target.build_reason` is set):
- Label: `BUILD REASON` (9px uppercase, muted)
- Value: the explain string, e.g. `meta change: sfcgal, sfcgal-devel`
- If `build_reason_packages` is non-empty, the packages are appended as a comma-separated list after the explain string

**2. State-specific section** (shown based on state):

| State | Section label | Source field | Colour |
|---|---|---|---|
| `blocked` | `WAITING FOR` | `target.blocked_by` | amber |
| `finished` (succeeded/unchanged) | `BUILD OUTCOME` | `target.details` | green |
| `finished` (failed) | `BUILD OUTCOME` | `target.details` | red |
| `unresolvable` | `UNRESOLVABLE` | `target.details` | purple |
| `broken` | `BROKEN` | `target.details` | red/pink |
| `failed` | *(none — future work)* | build log | red |
| `building`, `scheduled` | *(none)* | — | — |

If neither section has content, the chevron is hidden and the body is never shown.

### hasDetail logic

A helper `hasDetail(t: Target): boolean` returns true when:
- `t.build_reason` is non-empty, OR
- the state-specific section has a value (`t.blocked_by` for blocked; `t.details` for finished/unresolvable/broken)

The chevron renders only when `hasDetail(t)` is true.

### State colours (unchanged from current)

| State | Background | Dot/label |
|---|---|---|
| `failed` | `#2a1818` | `#f44747` |
| `unresolvable` | `#1e1828` | `#9d7cd8` |
| `broken` | `#28181e` | `#ff757f` |
| `blocked` | `#1e1e14` | `#e0af68` |
| `building` | `#141b28` | `#7aa2f7` |
| `scheduled` | `#141b28` | `#7aa2f7` |
| `finished` (unknown) | `#1e1e10` | `#e0af68` |
| `finished` (ok) | green-tinted | `#9ece6a` |
| `finished` (fail) | red-tinted | `#f44747` |
| `succeeded` | `#141e14` | `#9ece6a` |

## Scope

**Frontend only.** No backend changes — all fields (`details`, `blocked_by`, `build_reason`, `build_reason_packages`) are already in the API response and the TypeScript types.

**File:** `frontend/src/components/PackageCard.vue` only.

**Removed:** the current flat `<span>` approach for state detail (the three conditional span lines added in recent commits).

**Not in scope:** build log extraction for `failed` state — the `Error` section in the mockup is a placeholder for future work.

## Implementation notes

- Use `v-show` (not `v-if`) for the body to avoid re-mount flicker on toggle
- The `expandedTargets` Set should be a `ref<Set<string>>` to be reactive; toggle by creating a new Set on mutation so Vue detects the change
- `hasDetail` should be a method on the component (not a computed) since it takes a target argument
- The `log ↗` link URL pattern follows the existing OBS URL construction used elsewhere in `PackageCard.vue`
- `unchanged` is treated as a successful outcome (green), matching the existing treatment in the current code
