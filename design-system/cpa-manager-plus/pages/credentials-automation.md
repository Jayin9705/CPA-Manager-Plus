# Credentials and automation override

Applies to `/auth-files`, `/oauth`, `/quota`, `/codex-inspection*`, and
`/monitoring/account-actions`. All unspecified rules inherit `../MASTER.md`.

## Structure

- Credentials default to a searchable table/list with provider, owner, plan, state,
  quota windows, last check, and recommended action.
- Inspection is a run-oriented workflow: configuration → progress → results → reviewed
  actions. Historical runs remain reachable.
- Bulk actions use a selection bar and show selected/eligible/skipped counts.
- OAuth uses a step state: provider selected, external authorization, callback pending,
  completed/failed with recovery.

## Rules

- Disabled, cooldown, expired, reauth-required, and manually-disabled are distinct.
- Automation explains why an action was suggested and who/what last changed the state.
- Browser-local inspection may retain browser-side scheduling and presentation, but
  all network probes in full mode go through the authenticated Go API; provider
  credentials are never sent to or persisted by the console.
- Destructive bulk actions require a review screen and are never executed from a row
  checkbox alone.
- Quota uses text + progress + reset time; unknown is never rendered as zero or healthy.
- Preserve the current browser-local and server-side inspection capabilities.
