# Usage analytics override

Applies to `/usage-analytics` and `/model-prices`. All unspecified rules inherit
`../MASTER.md`.

## Structure

- Keep overview, trend, model, client-key, credential, and heatmap views as deep-linkable
  tabs.
- Use one shared sticky filter/query bar across tabs; filter changes update the URL.
- Summary cards are limited to six and remain definitionally consistent between tabs.
- Use line charts for time trends, horizontal sorted bars for rankings, and a heatmap
  only for hour/day intensity.
- An anomaly list accompanies charts and links to the matching request-monitor filter.

## Rules

- Never put request count, token count, and currency on an unlabeled shared axis.
- At most two y-axes; each axis uses an explicit unit and matching series label.
- Provide CSV export and an accessible table for every analytical result.
- Empty periods show the selected time range and a link to collector diagnostics.
