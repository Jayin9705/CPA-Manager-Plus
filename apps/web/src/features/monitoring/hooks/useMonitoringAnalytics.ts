import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  useRequestMonitoringAvailability,
  type RequestMonitoringUnavailableReason,
} from '@/hooks/useRequestMonitoringAvailability';
import {
  monitoringAnalyticsApi,
  type MonitoringAnalyticsEventsPageRequest,
  type MonitoringAnalyticsFilters,
  type MonitoringAnalyticsInclude,
  type MonitoringAnalyticsRequest,
  type MonitoringAnalyticsResponse,
} from '@/services/api/usageService';
import { useAuthStore } from '@/stores';

const DEFAULT_REFRESH_THROTTLE_MS = 5_000;

type SharedAnalyticsRequest = {
  controller: AbortController;
  promise: Promise<MonitoringAnalyticsResponse>;
  subscribers: number;
  settled: boolean;
};

// Several panels can ask for the same analytics snapshot while a route is
// mounting (for example a pre-existing transition layer and the new layer).
// Keep one transport request per exact payload and only abort it when its last
// consumer goes away. This prevents browser connection queueing without
// changing the response shape or freshness semantics.
const sharedAnalyticsRequests = new Map<string, SharedAnalyticsRequest>();

const buildSharedAnalyticsRequestKey = (
  serviceBase: string,
  managementKey: string | undefined,
  requestKey: string
) => `${serviceBase}\u0000${managementKey || ''}\u0000${requestKey}`;

const acquireSharedAnalyticsRequest = (
  serviceBase: string,
  managementKey: string | undefined,
  request: MonitoringAnalyticsRequest,
  requestKey: string
) => {
  const key = buildSharedAnalyticsRequestKey(serviceBase, managementKey, requestKey);
  let entry = sharedAnalyticsRequests.get(key);
  if (!entry) {
    const controller = new AbortController();
    let requestPromise: Promise<MonitoringAnalyticsResponse>;
    try {
      requestPromise = monitoringAnalyticsApi.getAnalytics(
        serviceBase,
        managementKey,
        request,
        controller.signal
      );
    } catch (error) {
      requestPromise = Promise.reject(error);
    }

    entry = {
      controller,
      promise: requestPromise,
      subscribers: 0,
      settled: false,
    };
    const currentEntry = entry;
    entry.promise = requestPromise.finally(() => {
      currentEntry.settled = true;
      if (sharedAnalyticsRequests.get(key) === currentEntry) {
        sharedAnalyticsRequests.delete(key);
      }
    });
    sharedAnalyticsRequests.set(key, entry);
  }

  entry.subscribers += 1;
  let released = false;
  const release = () => {
    if (released) return;
    released = true;
    entry!.subscribers = Math.max(0, entry!.subscribers - 1);
    if (entry!.subscribers === 0 && !entry!.settled) {
      if (sharedAnalyticsRequests.get(key) === entry) {
        sharedAnalyticsRequests.delete(key);
      }
      entry!.controller.abort();
    }
  };

  return { promise: entry.promise, release, signal: entry.controller.signal };
};

export interface UseMonitoringAnalyticsParams {
  enabled?: boolean;
  fromMs?: number | null;
  toMs?: number | null;
  nowMs?: number;
  dataScopeKey?: string;
  searchQuery?: string;
  searchApiKeyHash?: string;
  filters?: MonitoringAnalyticsFilters;
  include?: MonitoringAnalyticsInclude;
  eventsPage?: MonitoringAnalyticsEventsPageRequest | null;
  throttleMs?: number;
}

export interface MonitoringAnalyticsRefreshOptions {
  force?: boolean;
}

export interface UseMonitoringAnalyticsReturn {
  enabled: boolean;
  loading: boolean;
  error: string;
  data: MonitoringAnalyticsResponse | null;
  dataStale: boolean;
  lastRefreshedAt: Date | null;
  serviceBase: string;
  unavailableReason: RequestMonitoringUnavailableReason | '';
  refresh: (options?: MonitoringAnalyticsRefreshOptions) => Promise<void>;
}

const isFiniteTimestamp = (value: number | null | undefined): value is number =>
  typeof value === 'number' && Number.isFinite(value);

const stableJson = (value: unknown) => JSON.stringify(value ?? {});

const parseJson = <T>(value: string): T => JSON.parse(value) as T;

const getBrowserTimeZone = () => {
  if (typeof Intl === 'undefined') return '';
  return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
};

const buildInFlightRequestIdentityKey = (
  dataScopeKey: string | undefined,
  request: MonitoringAnalyticsRequest,
  requestKey: string
) => {
  if (!dataScopeKey) {
    return requestKey;
  }

  const eventsPage = request.include?.events_page;
  return stableJson({
    dataScopeKey,
    eventsPage: eventsPage
      ? {
          limit: eventsPage.limit ?? null,
          before_ms: eventsPage.before_ms ?? null,
          before_id: eventsPage.before_id ?? null,
        }
      : null,
  });
};

export function useMonitoringAnalytics({
  enabled: requestedEnabled = true,
  fromMs,
  toMs,
  nowMs,
  dataScopeKey,
  searchQuery,
  searchApiKeyHash,
  filters,
  include,
  eventsPage,
  throttleMs = DEFAULT_REFRESH_THROTTLE_MS,
}: UseMonitoringAnalyticsParams): UseMonitoringAnalyticsReturn {
  const managementKey = useAuthStore((state) => state.managementKey);
  const availability = useRequestMonitoringAvailability();
  const [data, setData] = useState<MonitoringAnalyticsResponse | null>(null);
  const [dataScopeStateKey, setDataScopeStateKey] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [lastRefreshedAt, setLastRefreshedAt] = useState<Date | null>(null);
  const requestIdRef = useRef(0);
  const lastStartedAtRef = useRef(0);
  const lastRequestKeyRef = useRef('');
  const inFlightRequestIdentityKeyRef = useRef('');
  const inFlightRequestIdRef = useRef(0);
  const releaseInFlightRequestRef = useRef<(() => void) | null>(null);

  const filtersKey = useMemo(() => stableJson(filters), [filters]);
  const includeKey = useMemo(() => stableJson(include), [include]);
  const eventsPageKey = useMemo(() => JSON.stringify(eventsPage ?? null), [eventsPage]);

  const request = useMemo<MonitoringAnalyticsRequest | null>(() => {
    if (!isFiniteTimestamp(fromMs) || !isFiniteTimestamp(toMs) || fromMs <= 0 || fromMs >= toMs) {
      return null;
    }

    const nextFilters = parseJson<MonitoringAnalyticsFilters>(filtersKey);
    const nextInclude = parseJson<MonitoringAnalyticsInclude>(includeKey);
    const nextEventsPage = parseJson<MonitoringAnalyticsEventsPageRequest | null>(eventsPageKey);
    if (nextEventsPage) {
      nextInclude.events_page = nextEventsPage;
    }

    const payload: MonitoringAnalyticsRequest = {
      from_ms: fromMs,
      to_ms: toMs,
    };
    const timeZone = getBrowserTimeZone();
    if (timeZone) {
      payload.time_zone = timeZone;
    }
    if (isFiniteTimestamp(nowMs)) {
      payload.now_ms = nowMs;
    }

    const normalizedSearchQuery = searchQuery?.trim();
    if (normalizedSearchQuery) {
      payload.search_query = normalizedSearchQuery;
    }

    const normalizedApiKeyHash = searchApiKeyHash?.trim();
    if (normalizedApiKeyHash) {
      payload.search_api_key_hash = normalizedApiKeyHash;
    }

    if (Object.keys(nextFilters).length > 0) {
      payload.filters = nextFilters;
    }
    if (Object.keys(nextInclude).length > 0) {
      payload.include = nextInclude;
    }
    return payload;
  }, [eventsPageKey, filtersKey, fromMs, includeKey, nowMs, searchApiKeyHash, searchQuery, toMs]);

  const requestKey = useMemo(() => (request ? stableJson(request) : ''), [request]);
  const inFlightRequestIdentityKey = useMemo(
    () => (request ? buildInFlightRequestIdentityKey(dataScopeKey, request, requestKey) : ''),
    [dataScopeKey, request, requestKey]
  );
  const activeDataScopeKey = dataScopeKey || requestKey;
  const serviceBase = availability.serviceBase;
  const enabled =
    requestedEnabled && availability.available && Boolean(serviceBase) && Boolean(request);

  const refresh = useCallback(
    async (options: MonitoringAnalyticsRefreshOptions = {}) => {
      if (!enabled || !request || !serviceBase) {
        releaseInFlightRequestRef.current?.();
        releaseInFlightRequestRef.current = null;
        requestIdRef.current += 1;
        inFlightRequestIdentityKeyRef.current = '';
        inFlightRequestIdRef.current = 0;
        setData(null);
        setDataScopeStateKey('');
        setLastRefreshedAt(null);
        setLoading(false);
        return;
      }

      if (inFlightRequestIdentityKeyRef.current === inFlightRequestIdentityKey) {
        return;
      }

      const startedAt = Date.now();
      const isSameRequest = lastRequestKeyRef.current === requestKey;
      if (
        !options.force &&
        isSameRequest &&
        startedAt - lastStartedAtRef.current < Math.max(0, throttleMs)
      ) {
        return;
      }

      const requestId = requestIdRef.current + 1;
      releaseInFlightRequestRef.current?.();
      releaseInFlightRequestRef.current = null;
      requestIdRef.current = requestId;
      lastStartedAtRef.current = startedAt;
      lastRequestKeyRef.current = requestKey;
      inFlightRequestIdentityKeyRef.current = inFlightRequestIdentityKey;
      inFlightRequestIdRef.current = requestId;
      setLoading(true);
      setError('');

      const sharedRequest = acquireSharedAnalyticsRequest(
        serviceBase,
        managementKey,
        request,
        requestKey
      );
      releaseInFlightRequestRef.current = sharedRequest.release;

      try {
        const response = await sharedRequest.promise;
        if (requestIdRef.current !== requestId) return;
        setData(response);
        setDataScopeStateKey(activeDataScopeKey);
        setLastRefreshedAt(new Date());
      } catch (err) {
        if (sharedRequest.signal.aborted) return;
        if (requestIdRef.current !== requestId) return;
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        sharedRequest.release();
        if (releaseInFlightRequestRef.current === sharedRequest.release) {
          releaseInFlightRequestRef.current = null;
        }
        if (inFlightRequestIdRef.current === requestId) {
          inFlightRequestIdentityKeyRef.current = '';
          inFlightRequestIdRef.current = 0;
        }
        if (requestIdRef.current === requestId) {
          setLoading(false);
        }
      }
    },
    [
      activeDataScopeKey,
      enabled,
      inFlightRequestIdentityKey,
      managementKey,
      request,
      requestKey,
      serviceBase,
      throttleMs,
    ]
  );

  useEffect(() => {
    if (availability.checking) {
      return;
    }
    void refresh({ force: true });
  }, [availability.checking, refresh]);

  useEffect(
    () => () => {
      releaseInFlightRequestRef.current?.();
      releaseInFlightRequestRef.current = null;
    },
    []
  );

  const dataStale = Boolean(dataScopeKey && data && dataScopeStateKey !== activeDataScopeKey);
  const scopedData = dataScopeKey || dataScopeStateKey === activeDataScopeKey ? data : null;

  return useMemo(
    () => ({
      enabled,
      loading: availability.checking || loading,
      error,
      data: scopedData,
      dataStale,
      lastRefreshedAt,
      serviceBase,
      unavailableReason: availability.reason,
      refresh,
    }),
    [
      availability.checking,
      availability.reason,
      enabled,
      error,
      dataStale,
      lastRefreshedAt,
      loading,
      refresh,
      scopedData,
      serviceBase,
    ]
  );
}
