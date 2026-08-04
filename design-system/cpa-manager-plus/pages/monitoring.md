# Monitoring override

Applies to `/monitoring`, `/monitoring/account-actions`, and request-history views.
All unspecified rules inherit `../MASTER.md`.

## Structure

1. Sticky query bar: time range, saved/active filter summary, search, refresh cadence,
   pause/resume, export/import.
2. Collapsible advanced filters. Active filters are visible as removable tokens.
3. Compact KPI summary limited to requests, failures, success rate, cost, token total,
   and P95 latency. Additional metrics live in a details popover.
4. Main result area uses tabs for account, API key, and real-time events.
5. Selecting a row opens a right detail drawer with trace, token breakdown, latency,
   redacted failure evidence, and related-account navigation.

## Rules

- The event table is the primary artifact; charts and KPIs must not push it below two
  desktop viewport folds.
- Auto-refresh defaults to the existing cadence, clearly shows last success, and can
  be paused without losing filter state.
- Virtualize 100+ rows and request the next cursor before the user reaches the end.
- On mobile, rows become a priority summary (status, model/account, time, latency,
  cost); all remaining fields move to the detail drawer.
- Failure state uses icon + label + code. Never expose raw provider failure bodies.
