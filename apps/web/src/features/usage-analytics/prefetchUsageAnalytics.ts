import {
  monitoringAnalyticsApi,
  type MonitoringAnalyticsRequest,
} from '@/services/api/usageService';
import {
  buildUsageAnalyticsFilters,
  buildUsageAnalyticsInclude,
  getUsageRangeBounds,
  resolveUsageGranularity,
} from './usageAnalyticsModel';
import {
  buildUsageAnalyticsUiStateFromSearchParams,
  readUsageAnalyticsUiState,
} from './usageAnalyticsUiState';

const PREFETCH_SUPPRESSION_MS = 10_000;

let lastPrefetchKey = '';
let lastPrefetchAt = 0;
let inFlightPrefetchKey = '';

const getBrowserTimeZone = () => {
  if (typeof Intl === 'undefined') return '';
  return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
};

const buildPrefetchRequest = (search: string | undefined): MonitoringAnalyticsRequest | null => {
  const persistedState = readUsageAnalyticsUiState();
  const state = search
    ? buildUsageAnalyticsUiStateFromSearchParams(new URLSearchParams(search), persistedState)
    : persistedState;
  const nowMs = Date.now();
  const bounds = getUsageRangeBounds(state.filters, nowMs);
  if (!bounds) return null;

  const granularity = resolveUsageGranularity(state.filters, nowMs);
  const filters = buildUsageAnalyticsFilters(state.filters);
  const request: MonitoringAnalyticsRequest = {
    from_ms: bounds.fromMs,
    to_ms: bounds.toMs,
    now_ms: nowMs,
    include: buildUsageAnalyticsInclude(state.activeTab, granularity),
  };
  const timeZone = getBrowserTimeZone();
  if (timeZone) request.time_zone = timeZone;

  const searchQuery = state.filters.searchQuery.trim();
  if (searchQuery) request.search_query = searchQuery;
  if (Object.keys(filters).length > 0) request.filters = filters;
  return request;
};

/**
 * Warms the manager's analytics cache while the user is hovering/focusing the
 * sidebar item. The request matches the compact primary request used by the
 * page, so the click can join the server-side singleflight/cache.
 */
export const prefetchUsageAnalytics = (
  serviceBase: string,
  managementKey: string | undefined,
  search?: string
) => {
  if (!serviceBase) return;
  const request = buildPrefetchRequest(search);
  if (!request) return;

  const key = JSON.stringify({ serviceBase, request });
  const now = Date.now();
  if (
    key === inFlightPrefetchKey ||
    (key === lastPrefetchKey && now - lastPrefetchAt < PREFETCH_SUPPRESSION_MS)
  ) {
    return;
  }

  inFlightPrefetchKey = key;
  void monitoringAnalyticsApi
    .getAnalytics(serviceBase, managementKey, request)
    .catch(() => undefined)
    .finally(() => {
      if (inFlightPrefetchKey === key) inFlightPrefetchKey = '';
      lastPrefetchKey = key;
      lastPrefetchAt = Date.now();
    });
};
