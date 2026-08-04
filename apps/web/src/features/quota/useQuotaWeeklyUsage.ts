import { useCallback, useEffect, useRef, useState } from 'react';
import { monitoringAnalyticsApi, type QuotaWeeklyUsageTarget } from '@/services/api/usageService';
import { buildQuotaWeeklyUsageLookup, type QuotaWeeklyUsageLookup } from './quotaWeeklyUsage';

interface UseQuotaWeeklyUsageOptions {
  serviceBase: string;
  managementKey?: string;
  targets: QuotaWeeklyUsageTarget[];
}

export const useQuotaWeeklyUsage = ({
  serviceBase,
  managementKey,
  targets,
}: UseQuotaWeeklyUsageOptions) => {
  const [itemsByKey, setItemsByKey] = useState<QuotaWeeklyUsageLookup>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    const requestId = ++requestIdRef.current;
    abortRef.current?.abort();
    abortRef.current = null;

    if (!serviceBase || targets.length === 0) {
      setItemsByKey({});
      setLoading(false);
      setError('');
      return;
    }

    const controller = new AbortController();
    abortRef.current = controller;
    setItemsByKey({});
    setLoading(true);
    setError('');
    try {
      const response = await monitoringAnalyticsApi.getQuotaWeeklyUsage(
        serviceBase,
        managementKey,
        { targets },
        controller.signal
      );
      if (requestId !== requestIdRef.current) return;
      setItemsByKey(buildQuotaWeeklyUsageLookup(response.items));
    } catch (err: unknown) {
      if (controller.signal.aborted || requestId !== requestIdRef.current) return;
      setError(err instanceof Error ? err.message : 'Failed to load weekly usage');
    } finally {
      if (requestId === requestIdRef.current) {
        setLoading(false);
        abortRef.current = null;
      }
    }
  }, [managementKey, serviceBase, targets]);

  useEffect(() => {
    void load();
    return () => {
      requestIdRef.current += 1;
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [load]);

  return { itemsByKey, loading, error, refresh: load };
};
