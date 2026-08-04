import type { ServerOperationsSnapshot } from '@/services/api/usageService';

export const OPERATIONS_POLL_INTERVAL_MS = 5_000;
export const OPERATIONS_HISTORY_LIMIT = 60;

export const appendOperationsSample = (
  samples: ServerOperationsSnapshot[],
  sample: ServerOperationsSnapshot,
  limit = OPERATIONS_HISTORY_LIMIT
): ServerOperationsSnapshot[] => {
  const next = [...samples.filter((item) => item.timestampMs !== sample.timestampMs), sample].sort(
    (left, right) => left.timestampMs - right.timestampMs
  );
  return next.slice(-Math.max(1, limit));
};
