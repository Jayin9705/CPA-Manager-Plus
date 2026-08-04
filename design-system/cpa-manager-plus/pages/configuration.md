# Configuration override

Applies to `/config`, `/ai-providers/*`, `/plugins`, and provider/config editors.
All unspecified rules inherit `../MASTER.md`.

## Structure

- Desktop uses a master-detail layout: searchable section navigation on the left,
  focused editor on the right.
- Mobile uses full-page editors with predictable Back and a sticky action bar.
- Long provider forms use Basics → Authentication → Models/Routing → Advanced.
- YAML/raw mode and visual mode share one canonical draft and expose a reviewable diff.

## Rules

- Persistent action bar states: unchanged, unsaved, validating, saving, saved, conflict.
- Validate locally first, then use the backend validation endpoint before commit.
- Use `ETag`/`If-Match` conflict handling so stale tabs cannot silently overwrite data.
- Secret fields are masked, never prefilled with retrievable values, and distinguish
  “unchanged” from “replace”.
- Delete/disable actions show dependency impact and never share the primary Save color.
- Plugin install shows source, version, permissions/capabilities, and typed confirmation
  for untrusted sources.
