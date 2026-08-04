import { parseTimestamp } from './timestamp';

const UNKNOWN_METADATA_VALUES = new Set(['', 'n/a', 'na', 'none', 'null', 'unknown', 'undefined']);

export const normalizeServerMetadataValue = (value: unknown): string | null => {
  if (value === null || value === undefined) return null;

  const normalized = String(value).trim();
  if (UNKNOWN_METADATA_VALUES.has(normalized.toLowerCase())) return null;
  return normalized || null;
};

export const formatServerBuildDate = (value: unknown, locale?: string): string | null => {
  const normalized = normalizeServerMetadataValue(value);
  if (!normalized) return null;

  const date = parseTimestamp(normalized);
  if (!date) return null;
  return date.toLocaleString(locale);
};
