# CPA Manager Plus UI Design System

> Source of truth for the separated CPA Manager Plus console. This file was
> generated from `ui-ux-pro-max` recommendations and then calibrated against the
> existing React application, Chinese/English content, current screenshots, and
> the product's operational workflows.
>
> Before implementing a page, check `pages/<page-name>.md`. A page file may
> override this master only where it says so explicitly.

## 1. Product direction

- Product type: AI gateway operations console / analytics dashboard / developer tool.
- Primary users: operators and administrators managing CPA providers, credentials,
  usage, cost, health, automation, and plugins.
- Style: **Data-Dense Dashboard + Drill-Down Analytics + Real-Time Monitoring**.
- Variance: 5/10 — modern but predictable.
- Motion: 3/10 — subtle and functional.
- Density: 8/10 — compact desktop data views with touch-safe mobile controls.
- Theme: light and dark have equal support; follow the operating-system preference
  on first visit.

The console is an operational tool, not a marketing site. Trust comes from clear
state, fast feedback, stable layouts, and precise data—not glass effects, ambient
blobs, gradients, or decorative animation.

## 2. Non-negotiable principles

1. Preserve task context: URL, filters, table sort, selected row, and scroll position
   survive back navigation.
2. Show state explicitly: loading, empty, partial, stale, offline, unauthorized,
   forbidden, and failed are separate states.
3. One primary action per view. Destructive actions are spatially separated.
4. Never use color as the only status signal; pair it with text, icon, or pattern.
5. Every key screen has a deep link and a keyboard-reachable path.
6. Dense does not mean cramped: information may be compact, while interactive
   targets remain at least 44×44px on touch devices.
7. No secret, management key, raw failure body, or token is rendered unless the
   existing capability explicitly requires a masked/reveal interaction.

## 3. Semantic color tokens

Do not put raw hex values in page or feature components. Components consume the
semantic tokens below. Chart palettes are separate from application state colors.

### Light theme

| Token              | Value     | Use                                   |
| ------------------ | --------- | ------------------------------------- |
| `--bg-canvas`      | `#F8FAFC` | Application background                |
| `--bg-surface`     | `#FFFFFF` | Cards, panels, table                  |
| `--bg-subtle`      | `#F1F5F9` | Grouped controls, secondary rows      |
| `--bg-selected`    | `#EFF6FF` | Selected navigation/table row         |
| `--text-primary`   | `#0F172A` | Main text, contrast 17.06:1 on canvas |
| `--text-secondary` | `#475569` | Supporting text, contrast 7.24:1      |
| `--text-muted`     | `#64748B` | Metadata, contrast 4.76:1 on white    |
| `--border-subtle`  | `#E2E8F0` | Non-interactive separators            |
| `--border-strong`  | `#94A3B8` | Inputs and meaningful boundaries      |
| `--brand`          | `#1D4ED8` | Primary action, active navigation     |
| `--brand-hover`    | `#1E40AF` | Primary hover                         |
| `--brand-soft`     | `#DBEAFE` | Brand-tinted background               |
| `--focus-ring`     | `#2563EB` | 2–3px keyboard focus ring             |
| `--success`        | `#15803D` | Success/healthy text and icon         |
| `--success-soft`   | `#DCFCE7` | Success background                    |
| `--warning`        | `#B45309` | Warning text/icon; 5.02:1 with white  |
| `--warning-soft`   | `#FEF3C7` | Warning background                    |
| `--danger`         | `#B91C1C` | Error/destructive text and button     |
| `--danger-soft`    | `#FEE2E2` | Error background                      |
| `--info`           | `#0369A1` | Informational state                   |
| `--info-soft`      | `#E0F2FE` | Informational background              |

### Dark theme

| Token              | Value     | Use                                          |
| ------------------ | --------- | -------------------------------------------- |
| `--bg-canvas`      | `#020617` | Application background                       |
| `--bg-surface`     | `#0F172A` | Cards, panels, table                         |
| `--bg-subtle`      | `#1E293B` | Grouped controls, secondary rows             |
| `--bg-selected`    | `#172554` | Selected navigation/table row                |
| `--text-primary`   | `#F8FAFC` | Main text, contrast 19.28:1 on canvas        |
| `--text-secondary` | `#CBD5E1` | Supporting text, contrast 12.02:1 on surface |
| `--text-muted`     | `#94A3B8` | Metadata                                     |
| `--border-subtle`  | `#1E293B` | Non-interactive separators                   |
| `--border-strong`  | `#475569` | Inputs and meaningful boundaries             |
| `--brand`          | `#60A5FA` | Active/focus text on dark surfaces           |
| `--brand-button`   | `#2563EB` | Primary button; white contrast 5.17:1        |
| `--brand-hover`    | `#3B82F6` | Primary hover                                |
| `--brand-soft`     | `#1E3A8A` | Brand-tinted background                      |
| `--focus-ring`     | `#60A5FA` | Keyboard focus ring                          |
| `--success`        | `#4ADE80` | Healthy state                                |
| `--success-soft`   | `#14532D` | Healthy background                           |
| `--warning`        | `#FBBF24` | Warning state                                |
| `--warning-soft`   | `#713F12` | Warning background                           |
| `--danger`         | `#F87171` | Error/destructive state                      |
| `--danger-soft`    | `#7F1D1D` | Error background                             |
| `--info`           | `#38BDF8` | Informational state                          |
| `--info-soft`      | `#0C4A6E` | Informational background                     |

### Data visualization palette

- Series order: blue `#2563EB`, teal `#0F766E`, amber `#B45309`, violet
  `#7C3AED`, magenta `#BE185D`, slate `#475569`.
- Dark-theme series use lighter tonal counterparts.
- Maximum six simultaneous line series. Beyond six, require selection or split the
  chart.
- Success/warning/danger colors are reserved for state. Do not reuse them merely to
  distinguish neutral data series.
- Lines use solid/dashed/dotted patterns in addition to color. Heatmaps always show
  a numeric legend and have a table alternative.

## 4. Typography

### Families

- UI and headings: `Inter Variable`, `Noto Sans SC`, `Noto Sans TC`, `PingFang SC`,
  `Microsoft YaHei`, `system-ui`, sans-serif.
- Code, IDs, timestamps, token counts, prices, and tabular numbers: `Fira Code`,
  `JetBrains Mono`, `SFMono-Regular`, `Consolas`, monospace.
- Do not use a monospace font for Chinese headings or body copy.
- Self-host only the required Latin variable font subset. Prefer installed CJK
  system fonts; if Noto is self-hosted, subset by locale and use `font-display: swap`.

### Scale

| Token                | Size / line height | Weight | Use                                |
| -------------------- | ------------------ | ------ | ---------------------------------- |
| `--type-display`     | `32px / 40px`      | 700    | Rare overview headline             |
| `--type-h1`          | `24px / 32px`      | 650    | Page title                         |
| `--type-h2`          | `20px / 28px`      | 600    | Major section                      |
| `--type-h3`          | `16px / 24px`      | 600    | Card/panel title                   |
| `--type-body`        | `14px / 22px`      | 400    | Desktop body                       |
| `--type-body-mobile` | `16px / 24px`      | 400    | Mobile inputs/body                 |
| `--type-label`       | `13px / 20px`      | 500    | Field/table label                  |
| `--type-caption`     | `12px / 18px`      | 400    | Metadata only; never critical text |

Numeric columns use `font-variant-numeric: tabular-nums`. Long identifiers may be
truncated only when a tooltip/copy action exposes the full value.

## 5. Spacing, shape, elevation, and layers

### Spacing

Use a 4px base grid.

| Token       | Value  | Use                         |
| ----------- | ------ | --------------------------- |
| `--space-1` | `4px`  | Tight inline gap            |
| `--space-2` | `8px`  | Standard control gap        |
| `--space-3` | `12px` | Dense card/table padding    |
| `--space-4` | `16px` | Standard page/card padding  |
| `--space-5` | `20px` | Related section gap         |
| `--space-6` | `24px` | Page section separation     |
| `--space-8` | `32px` | Large structural separation |

### Shape and elevation

- Input/button radius: 6px.
- Card/table radius: 8px.
- Modal/drawer radius: 12px.
- Pills are reserved for status, tags, and segmented controls—not generic cards.
- Cards are not all clickable. Non-clickable cards have no hover lift or pointer
  cursor.
- Use borders for normal hierarchy. Shadows are reserved for floating menus,
  drawers, and modals.
- No decorative glass blur. A scrim may use a subtle blur only to separate a modal.

### Z-index scale

| Token          | Value | Layer                       |
| -------------- | ----- | --------------------------- |
| `--z-base`     | `0`   | Content                     |
| `--z-sticky`   | `20`  | Sticky filters/table header |
| `--z-header`   | `30`  | App header/sidebar          |
| `--z-dropdown` | `50`  | Menus, tooltips             |
| `--z-drawer`   | `70`  | Drawers/sheets              |
| `--z-modal`    | `90`  | Modal and scrim             |
| `--z-toast`    | `110` | Toast notifications         |

## 6. Application shell and responsive behavior

- Desktop ≥1024px: 240px grouped sidebar, collapsible to 64px; 56px header; main
  content uses a 12-column grid and a 1600px maximum width.
- Tablet 768–1023px: collapsed sidebar by default; filters may move into a drawer.
- Mobile <768px: no persistent sidebar. Use a top app bar and grouped navigation
  drawer. Do not squeeze the product's many destinations into a bottom bar.
- Breakpoints tested: 375, 768, 1024, and 1440px, plus mobile landscape.
- The page itself must never scroll horizontally. Wide data views switch to a
  priority-column summary with a detail drawer; a locally scrollable table is a
  last-resort enhancement, not the only mobile view.
- Every route change focuses the page heading/main region. Provide a skip-to-content
  link before the primary navigation.

## 7. Component behavior

### Buttons and controls

- Desktop control height: 36px compact, 40px standard.
- Touch layouts: minimum 44×44px hit target with at least 8px between targets.
- Async action: retain label, add progress indicator, set `aria-busy`, and prevent
  duplicate submission.
- Disabled controls use native `disabled`, remain legible, and explain the reason
  when it is not obvious.
- Icon-only controls are limited to universal actions and always have an accessible
  name and tooltip.
- Use one outline icon family: Phosphor regular, 20px, consistent stroke/weight.

### Forms

- Visible label for every field; placeholders are examples, never labels.
- Validate on blur and on submit, not on every keystroke.
- Put an error beneath its field and focus the first invalid field after submit.
- Complex forms use sections and progressive disclosure. Persistent actions show
  dirty/saving/saved/conflict states.
- Long configuration edits keep an autosaved local draft and confirm before
  discarding unsaved work.
- Destructive actions require explicit confirmation; irreversible bulk actions also
  show the affected count and support undo where the backend can safely do so.

### Tables and lists

- Desktop default row height: 40px; optional comfortable mode: 48px.
- Sticky header, sortable columns with `aria-sort`, keyboard row navigation, column
  visibility, and persisted density preference.
- Virtualize after 100 visible rows. Use server cursor pagination for unbounded data.
- Row click opens a detail drawer; actions remain explicit buttons/menus and do not
  rely on row click alone.
- Loading shows reserved skeleton rows; empty/error/stale states never render a blank
  table.

### Feedback

- Toasts use `aria-live="polite"`, do not steal focus, and auto-dismiss after 4–5s.
- Errors state the cause and recovery action. Timeouts offer retry.
- Offline/API-unavailable state is persistent and distinct from an empty dataset.
- Auto-refresh always shows cadence, last successful refresh, stale state, and a
  pause/resume control.

### Charts

- Continue using ECharts; do not replace a functioning chart library during the
  separation migration.
- Trend → line; category comparison → sorted bar; time/day intensity → heatmap.
- Show units on axes, exact values in pointer and keyboard tooltips, a visible legend,
  and locale-aware number/time formatting.
- Provide an adjacent summary and a toggleable accessible data table.
- Use SVG for normal views; use Canvas/downsampling for 1,000+ points.
- Chart transitions are disabled by `prefers-reduced-motion` and never delay the
  readable state.

## 8. Motion

- Fast feedback: 120ms; standard transition: 180ms; drawer/modal enter: 240ms;
  exits are 60–70% of enter duration.
- Animate only `opacity` and `transform` where possible.
- Motion must explain state change: drawer origin, accordion expansion, refreshed
  content crossfade, or action completion.
- No scroll reveal, count-up KPI, looping ambient animation, flashing live dots, or
  decorative page transitions in operational views.
- `prefers-reduced-motion: reduce` removes non-essential motion and leaves state
  changes immediately readable.

## 9. Accessibility acceptance

- WCAG 2.2 AA minimum.
- Normal text contrast ≥4.5:1; large text and meaningful graphical objects ≥3:1.
- Visible 2–3px focus ring; never remove outline without a replacement.
- Complete keyboard operation, logical tab order, Escape dismissal, focus trapping,
  and return focus after modal/drawer close.
- Status never depends on red/green alone.
- Charts have text summaries and table alternatives.
- Errors use `role="alert"`; live updates use appropriately throttled `aria-live`.
- Headings are sequential; landmarks and labels are semantic.
- Zoom to 200% and text resizing must not hide actions or truncate critical content.

## 10. Performance budgets

- Route-level lazy loading remains mandatory.
- Initial application shell ≤250KB gzip, excluding lazily loaded ECharts/editor
  routes; individual route chunks target ≤200KB gzip.
- LCP <2.5s, CLS <0.1, INP <200ms at the 75th percentile on the agreed production
  profile.
- Reserve space for asynchronous cards/charts. Abort stale requests and debounce
  high-frequency filters.
- Profile before memoizing. Virtualize large tables and logs.

## 11. Forbidden patterns

- Emoji as structural icons.
- Raw hex colors inside feature/page components.
- Glassmorphism or blur as decoration.
- Hover-only functionality or layout-shifting hover transforms.
- Placeholder-only labels or errors shown only at page top.
- Full-page blocking spinners for independent panels.
- Red/green-only status, unlabeled icon buttons, invisible focus, or keyboard traps.
- Animated gradients, ambient blobs, auto-playing chart motion, and scroll-jacking.
- Rendering secrets, raw tokens, or unredacted provider failure bodies.

## 12. Pre-delivery checklist

- [ ] Tested at 375, 768, 1024, and 1440px, including mobile landscape.
- [ ] Tested light, dark, and system themes independently.
- [ ] Keyboard-only path completes every primary workflow.
- [ ] Screen-reader labels, heading hierarchy, live regions, and focus return verified.
- [ ] Contrast, 200% zoom, reduced motion, and touch targets verified.
- [ ] Loading, empty, partial, stale, offline, unauthorized, and error states verified.
- [ ] Long tables/logs are virtualized and retain sort/filter/back-navigation state.
- [ ] Every chart has units, exact values, accessible distinction, and a table/summary.
- [ ] No raw color, structural emoji, decorative blur, or layout-shifting motion remains.
- [ ] No secret is written to DOM, URL, logs, analytics, or browser persistence.
