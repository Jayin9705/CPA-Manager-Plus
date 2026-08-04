# Dashboard override

Applies to `/dashboard`. All unspecified rules inherit `../MASTER.md`.

## Purpose

Answer, in order: Is the control plane healthy? Is traffic healthy? What changed?
What requires action?

## Desktop structure

1. Compact environment bar: API/CPA connectivity, collector state, last refresh,
   refresh/pause action.
2. Four primary KPIs: requests, success rate, cost, P95 latency. Token details move
   into a secondary expandable row rather than creating eight equal KPI cards.
3. Two-column decision row: traffic/cost trend (8 columns) and active health alerts
   (4 columns).
4. Operational row: channel health, recent failures, collector/persistence status.
5. Version/build metadata stays in System; only show an upgrade alert here when an
   actionable mismatch exists.

## Rules

- KPIs show current value, comparison window, trend direction, and exact definition.
- Do not animate numbers on load.
- Health cards are ordered critical → warning → healthy; healthy detail is collapsed.
- Every alert links to a filtered destination rather than duplicating diagnostics.
- At 375px, render KPIs as a two-column grid and all analytical panels in one column.
