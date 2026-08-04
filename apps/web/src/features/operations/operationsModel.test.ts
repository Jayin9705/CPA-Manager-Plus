import { describe, expect, it } from 'vitest';
import type { ServerOperationsSnapshot } from '@/services/api/usageService';
import { appendOperationsSample } from './operationsModel';

const sample = (timestampMs: number, usagePercent = timestampMs): ServerOperationsSnapshot => ({
  timestampMs,
  cpu: { usagePercent, load1: 0, load5: 0, load15: 0, cores: 1 },
  memory: { totalBytes: 1, usedBytes: 0, availableBytes: 1, usagePercent: 0 },
  disk: {
    mountPoint: '/',
    totalBytes: 1,
    usedBytes: 0,
    availableBytes: 1,
    usagePercent: 0,
  },
  network: { receiveBytesPerSecond: 0, transmitBytesPerSecond: 0 },
  host: { hostname: 'test', operatingSystem: 'test', architecture: 'test', uptimeSeconds: 0 },
  process: { uptimeSeconds: 0, residentMemoryBytes: 0, goroutines: 0 },
});

describe('appendOperationsSample', () => {
  it('sorts, replaces duplicate timestamps, and keeps the requested window', () => {
    const result = appendOperationsSample([sample(2), sample(1), sample(3)], sample(2, 99), 3);

    expect(result.map((item) => item.timestampMs)).toEqual([1, 2, 3]);
    expect(result[1].cpu.usagePercent).toBe(99);

    const trimmed = appendOperationsSample(result, sample(4), 3);
    expect(trimmed.map((item) => item.timestampMs)).toEqual([2, 3, 4]);
  });
});
