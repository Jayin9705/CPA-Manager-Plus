# Login override

Applies to `/login` and first-run setup. All unspecified rules inherit
`../MASTER.md`.

## Structure and rules

- Single centered 420px panel on desktop; full-width with 20px gutters on mobile.
- Show product/environment identity and API availability without exposing internal
  addresses or versions unnecessarily.
- Full mode asks only for email, password, and remember-session. The API origin comes
  from runtime configuration, not a normal user field.
- Lightweight compatibility mode may expose CPA address and management key in a clearly
  labeled advanced/alternate mode.
- Password supports show/hide and autocomplete. Errors are field-specific, announced,
  and preserve the email value.
- Rate-limit responses display the remaining retry time. Backend outage and bad
  credentials are separate messages.
- After login, return to the originally requested safe internal route.
