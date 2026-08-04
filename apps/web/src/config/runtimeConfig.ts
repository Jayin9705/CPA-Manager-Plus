export type ConsoleRuntimeMode = 'full' | 'embedded' | 'demo';

export interface ConsoleRuntimeConfig {
  mode: ConsoleRuntimeMode;
  apiOrigin: string;
  oauthReturnOrigin?: string;
  pluginRuntimeOrigin?: string;
  environmentName?: string;
}

export class RuntimeConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RuntimeConfigError';
  }
}

let runtimeConfig: ConsoleRuntimeConfig = {
  mode: __DEMO_SITE__ ? 'demo' : 'embedded',
  apiOrigin: '',
};

const normalizeOrigin = (value: unknown, field: string): string => {
  if (typeof value !== 'string' || !value.trim()) {
    throw new RuntimeConfigError(`${field} is required`);
  }
  let parsed: URL;
  try {
    parsed = new URL(value.trim());
  } catch {
    throw new RuntimeConfigError(`${field} must be a valid http(s) URL`);
  }
  if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) {
    throw new RuntimeConfigError(`${field} must be an http(s) origin without credentials`);
  }
  if (parsed.search || parsed.hash) {
    throw new RuntimeConfigError(`${field} must not contain a query or hash`);
  }
  return parsed.origin + parsed.pathname.replace(/\/+$/, '');
};

const parseConfig = (value: unknown): ConsoleRuntimeConfig => {
  if (!value || typeof value !== 'object') {
    throw new RuntimeConfigError('runtime-config.json must contain an object');
  }
  const record = value as Record<string, unknown>;
  const mode = record.mode === 'full' ? 'full' : 'embedded';
  if (mode !== 'full') {
    throw new RuntimeConfigError(
      'runtime-config.json mode must be "full" for the separated console'
    );
  }
  return {
    mode,
    apiOrigin: normalizeOrigin(record.apiOrigin, 'apiOrigin'),
    oauthReturnOrigin:
      record.oauthReturnOrigin === undefined
        ? undefined
        : normalizeOrigin(record.oauthReturnOrigin, 'oauthReturnOrigin'),
    pluginRuntimeOrigin:
      record.pluginRuntimeOrigin === undefined
        ? undefined
        : normalizeOrigin(record.pluginRuntimeOrigin, 'pluginRuntimeOrigin'),
    environmentName:
      typeof record.environmentName === 'string'
        ? record.environmentName.trim() || undefined
        : undefined,
  };
};

export async function loadRuntimeConfig(): Promise<ConsoleRuntimeConfig> {
  if (__DEMO_SITE__) {
    runtimeConfig = { mode: 'demo', apiOrigin: '' };
    return runtimeConfig;
  }
  if (!__SEPARATED_SITE__) {
    runtimeConfig = { mode: 'embedded', apiOrigin: '' };
    return runtimeConfig;
  }

  const envOrigin = import.meta.env.VITE_API_ORIGIN;
  if (import.meta.env.DEV && envOrigin) {
    runtimeConfig = parseConfig({ mode: 'full', apiOrigin: envOrigin });
    return runtimeConfig;
  }

  let response: Response;
  try {
    const configURL = new URL(
      'runtime-config.json',
      `${window.location.origin}${import.meta.env.BASE_URL}`
    );
    response = await fetch(configURL, { cache: 'no-store', credentials: 'omit' });
  } catch {
    throw new RuntimeConfigError('Unable to load runtime-config.json');
  }
  if (!response.ok) {
    throw new RuntimeConfigError(`runtime-config.json returned HTTP ${response.status}`);
  }
  let value: unknown;
  try {
    value = await response.json();
  } catch {
    throw new RuntimeConfigError('runtime-config.json is not valid JSON');
  }
  runtimeConfig = parseConfig(value);
  return runtimeConfig;
}

export const getRuntimeConfig = (): ConsoleRuntimeConfig => runtimeConfig;
export const getRuntimeApiOrigin = (): string => runtimeConfig.apiOrigin;
export const isSeparatedSite = (): boolean => runtimeConfig.mode === 'full';
