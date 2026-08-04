import { normalizeApiBase } from '@/utils/connection';
import { clearCsrfToken, csrfHeaders, setCsrfToken } from '@/services/security/csrf';

export interface UsageServiceInfo {
  service?: string;
  mode?: string;
  startedAt?: number;
  configured?: boolean;
  adminReady?: boolean;
  projectInitialized?: boolean;
  setupRequired?: boolean;
  migrationStatus?: string;
  dataKeyReady?: boolean;
  hasHistoricalData?: boolean;
}

export interface ManagerLoginRequest {
  email: string;
  password: string;
  remember?: boolean;
}

export interface ManagerSessionResponse {
  authenticated: true;
  user: { id: number; email: string };
  expiresAt: number;
  csrfToken?: string;
}

export interface UsageServiceSetupRequest {
  cpaBaseUrl: string;
  cpaManagementKey: string;
  pollIntervalMs?: number;
  ensureUsageStatisticsEnabled?: boolean;
  requestMonitoringEnabled?: boolean;
}

export interface LoginServiceError extends Error {
  status?: number;
  code?: string;
  details?: unknown;
  data?: unknown;
}

export const USAGE_SERVICE_ID = 'cpa-manager-plus';
export const LEGACY_USAGE_SERVICE_ID = 'cpa-manager';
export const LEGACY_USAGE_SERVICE_IDS = [LEGACY_USAGE_SERVICE_ID, 'cpa-usage-service'] as const;
export const USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-manager-plus:last-cpa-base';
export const LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-manager:last-cpa-base';

const USAGE_SERVICE_ERROR_CODES = new Set([
  'request_failed',
  'connection_env_managed',
  'cpa_connection_required',
  'cpa_connection_required_for_monitoring',
  'management_api_validation_failed',
  'management_api_config_failed',
  'cpa_usage_retention_invalid',
  'poll_interval_exceeds_retention',
  'invalid_time_zone',
  'enable_cpa_usage_statistics_failed',
  'setup_env_managed',
  'invalid_existing_management_key',
  'invalid_admin_key',
  'invalid_management_key',
  'usage_service_not_configured',
  'method_not_allowed',
]);

export const isUsageServiceId = (service?: string): boolean =>
  service === USAGE_SERVICE_ID ||
  (typeof service === 'string' &&
    (LEGACY_USAGE_SERVICE_IDS as readonly string[]).includes(service));

const buildUrl = (base: string, path: string): string =>
  `${normalizeApiBase(base).replace(/\/+$/, '')}${path}`;

const errorCodeFromData = (value: unknown): string => {
  if (
    !value ||
    typeof value !== 'object' ||
    typeof (value as { code?: unknown }).code !== 'string'
  ) {
    return '';
  }
  const code = (value as { code: string }).code;
  return USAGE_SERVICE_ERROR_CODES.has(code) ? code : '';
};

const errorMessageFromData = (value: unknown): string => {
  if (!value || typeof value !== 'object') return '';
  const record = value as { error?: unknown; message?: unknown };
  return typeof record.error === 'string'
    ? record.error
    : typeof record.message === 'string'
      ? record.message
      : '';
};

const statusFallbackCode = (status: number): string => {
  if (status === 401) return 'invalid_admin_key';
  if (status === 405) return 'method_not_allowed';
  if (status === 412) return 'usage_service_not_configured';
  return '';
};

const toLoginServiceError = (
  message: string,
  status?: number,
  data?: unknown,
  code?: string
): LoginServiceError => {
  const error = new Error(message || 'Manager Server request failed') as LoginServiceError;
  error.name = 'LoginServiceError';
  error.status = status;
  error.data = data;
  error.details = data;
  error.code = code || (status ? statusFallbackCode(status) : undefined);
  return error;
};

const requestJson = async <T>(
  url: string,
  init: RequestInit = {},
  timeoutMs: number
): Promise<T> => {
  const controller = typeof AbortController !== 'undefined' ? new AbortController() : undefined;
  const timeout = controller ? window.setTimeout(() => controller.abort(), timeoutMs) : undefined;

  try {
    const response = await fetch(url, {
      ...init,
      credentials: 'include',
      signal: controller?.signal,
      headers: {
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
        ...csrfHeaders(init.method || 'GET'),
        ...init.headers,
      },
    });
    const text = await response.text();
    let data: unknown = undefined;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = text;
      }
    }
    if (!response.ok) {
      throw toLoginServiceError(
        errorMessageFromData(data) || response.statusText,
        response.status,
        data,
        errorCodeFromData(data) || statusFallbackCode(response.status)
      );
    }
    if (
      data &&
      typeof data === 'object' &&
      typeof (data as { csrfToken?: unknown }).csrfToken === 'string'
    ) {
      setCsrfToken((data as { csrfToken: string }).csrfToken);
    }
    return data as T;
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') {
      throw toLoginServiceError('Request timed out', undefined, undefined, 'ECONNABORTED');
    }
    throw error;
  } finally {
    if (timeout !== undefined) window.clearTimeout(timeout);
  }
};

export const getUsageServiceErrorCode = (error: unknown): string => {
  if (!error || typeof error !== 'object') return '';
  const record = error as { code?: unknown; data?: unknown; details?: unknown; status?: unknown };
  const directCode = typeof record.code === 'string' ? record.code : '';
  return (
    (USAGE_SERVICE_ERROR_CODES.has(directCode) ? directCode : '') ||
    errorCodeFromData(record.data) ||
    errorCodeFromData(record.details) ||
    (typeof record.status === 'number' ? statusFallbackCode(record.status) : '')
  );
};

export const loginServiceApi = {
  getInfo: (base: string, timeoutMs = 5_000) =>
    requestJson<UsageServiceInfo>(buildUrl(base, '/usage-service/info'), {}, timeoutMs),

  login: (base: string, credentials: ManagerLoginRequest) =>
    requestJson<ManagerSessionResponse>(
      buildUrl(base, '/usage-service/auth/login'),
      { method: 'POST', body: JSON.stringify(credentials) },
      30_000
    ),

  setup: async (base: string, payload: UsageServiceSetupRequest): Promise<void> => {
    await requestJson<unknown>(
      buildUrl(base, '/setup'),
      {
        method: 'POST',
        body: JSON.stringify(payload),
      },
      30_000
    );
  },
};

export const clearLoginCsrfToken = clearCsrfToken;
