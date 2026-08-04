import { describe, expect, it } from 'vitest';
import { formatServerBuildDate, normalizeServerMetadataValue } from './serverMetadata';

describe('server metadata', () => {
  it('treats placeholder metadata as unavailable', () => {
    expect(normalizeServerMetadataValue(' unknown ')).toBeNull();
    expect(normalizeServerMetadataValue('N/A')).toBeNull();
    expect(formatServerBuildDate('unknown', 'en-US')).toBeNull();
  });

  it('formats valid build timestamps and rejects invalid values', () => {
    expect(formatServerBuildDate('2026-07-29T00:00:00Z', 'en-US')).toBeTruthy();
    expect(formatServerBuildDate('not-a-date', 'en-US')).toBeNull();
  });
});
