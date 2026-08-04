import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type StorageLike = {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
  removeItem: (key: string) => void;
  clear: () => void;
};

const createMemoryStorage = (): StorageLike => {
  const store = new Map<string, string>();
  return {
    getItem: (key) => (store.has(key) ? (store.get(key) as string) : null),
    setItem: (key, value) => {
      store.set(key, value);
    },
    removeItem: (key) => {
      store.delete(key);
    },
    clear: () => {
      store.clear();
    },
  };
};

const apiClientSetConfig = vi.fn();
const fetchConfigMock = vi.fn();
const clearConfigCacheMock = vi.fn();
const clearModelsCacheMock = vi.fn();
const usageServiceGetManagerConfigMock = vi.fn();
const usageServiceLoginMock = vi.fn();
const usageServiceGetSessionMock = vi.fn();
const usageServiceLogoutMock = vi.fn();

vi.mock('@/services/api/client', () => ({
  apiClient: {
    setConfig: apiClientSetConfig,
  },
}));

vi.mock('./useConfigStore', () => ({
  useConfigStore: {
    getState: () => ({
      fetchConfig: fetchConfigMock,
      clearCache: clearConfigCacheMock,
    }),
  },
}));

vi.mock('./useModelsStore', () => ({
  useModelsStore: {
    getState: () => ({
      clearCache: clearModelsCacheMock,
    }),
  },
}));

vi.mock('@/services/api/usageService', async () => {
  const actual = await vi.importActual<typeof import('@/services/api/usageService')>(
    '@/services/api/usageService'
  );
  return {
    ...actual,
    usageServiceApi: {
      ...actual.usageServiceApi,
      getManagerConfig: usageServiceGetManagerConfigMock,
      login: usageServiceLoginMock,
      getSession: usageServiceGetSessionMock,
      logout: usageServiceLogoutMock,
    },
  };
});

describe('useAuthStore logout', () => {
  let storage: StorageLike;

  beforeEach(() => {
    vi.resetModules();
    apiClientSetConfig.mockClear();
    fetchConfigMock.mockReset();
    clearConfigCacheMock.mockClear();
    clearModelsCacheMock.mockClear();
    usageServiceGetManagerConfigMock.mockReset();
    usageServiceLoginMock.mockReset();
    usageServiceGetSessionMock.mockReset();
    usageServiceLogoutMock.mockReset();
    usageServiceLogoutMock.mockResolvedValue(undefined);
    storage = createMemoryStorage();
    vi.stubGlobal('localStorage', storage);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it('clears usage service config and resets api client credentials', async () => {
    const { useAuthStore } = await import('./useAuthStore');
    const { useUsageServiceStore } = await import('./useUsageServiceStore');

    useUsageServiceStore.getState().setUsageServiceConfig(
      {
        enabled: true,
        serviceBase: 'http://manager.local:18317/',
      },
      {
        panelBase: 'http://panel.local:8317',
        panelHostMode: 'external_panel',
      }
    );
    useAuthStore.setState({
      isAuthenticated: true,
      apiBase: 'http://cpa.local:8317',
      managementKey: 'management-key',
      connectionStatus: 'connected',
    });
    storage.setItem('isLoggedIn', 'true');

    await useAuthStore.getState().logout();

    expect(useUsageServiceStore.getState()).toMatchObject({
      enabled: false,
      serviceBase: '',
      panelBase: '',
      panelHostMode: '',
    });
    expect(apiClientSetConfig).toHaveBeenCalledWith({
      apiBase: '',
      managementKey: '',
      withCredentials: false,
    });
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: false,
      apiBase: '',
      managementKey: '',
      connectionStatus: 'disconnected',
    });
    expect(storage.getItem('isLoggedIn')).toBeNull();
  });

  it('revokes the Manager cookie session on logout', async () => {
    const { useAuthStore } = await import('./useAuthStore');
    let resolveLogout!: () => void;
    usageServiceLogoutMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveLogout = resolve;
        })
    );
    useAuthStore.setState({
      isAuthenticated: true,
      apiBase: 'http://manager.local:18317',
      managementKey: '',
      sessionMode: 'manager_embedded',
    });

    const logoutPromise = useAuthStore.getState().logout();

    await Promise.resolve();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);

    resolveLogout();
    await logoutPromise;

    expect(usageServiceLogoutMock).toHaveBeenCalledWith('http://manager.local:18317');
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: false,
      apiBase: '',
      managementKey: '',
      rememberPassword: false,
      sessionMode: '',
    });
  });
});

describe('useAuthStore manager embedded login recovery', () => {
  let storage: StorageLike;

  beforeEach(() => {
    vi.resetModules();
    apiClientSetConfig.mockClear();
    fetchConfigMock.mockReset();
    clearConfigCacheMock.mockClear();
    clearModelsCacheMock.mockClear();
    usageServiceGetManagerConfigMock.mockReset();
    usageServiceLoginMock.mockReset();
    usageServiceGetSessionMock.mockReset();
    usageServiceLogoutMock.mockReset();
    usageServiceLoginMock.mockResolvedValue({
      authenticated: true,
      user: { id: 1, email: 'admin@example.com' },
      expiresAt: Date.now() + 60_000,
    });
    usageServiceLogoutMock.mockResolvedValue(undefined);
    storage = createMemoryStorage();
    vi.stubGlobal('localStorage', storage);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it('uses a cookie session for Manager login and recovers through Manager config', async () => {
    fetchConfigMock.mockRejectedValue(new Error('invalid management key'));
    usageServiceGetManagerConfigMock.mockResolvedValue({
      config: {
        cpaConnection: {
          cpaBaseUrl: 'http://cpa.local:8317',
          managementKey: 'old-cpa-key',
        },
        collector: {
          enabled: false,
          collectorMode: 'auto',
          queue: 'usage',
          popSide: 'right',
          batchSize: 100,
          pollIntervalMs: 500,
          queryLimit: 50000,
        },
        externalUsageService: {
          enabled: false,
          serviceBase: '',
        },
      },
      source: 'db',
    });

    const { useAuthStore } = await import('./useAuthStore');
    const { useUsageServiceStore } = await import('./useUsageServiceStore');

    const result = await useAuthStore.getState().login({
      apiBase: 'http://manager.local:18317',
      email: 'admin@example.com',
      password: 'legacy-sub2-password',
      rememberPassword: true,
      sessionMode: 'manager_embedded',
      sessionPanelBase: 'http://manager.local:18317',
    });

    expect(usageServiceLoginMock).toHaveBeenCalledWith('http://manager.local:18317', {
      email: 'admin@example.com',
      password: 'legacy-sub2-password',
      remember: true,
    });
    expect(result).toEqual({ recoveryMode: 'manager_config' });
    expect(usageServiceGetManagerConfigMock).toHaveBeenCalledWith('http://manager.local:18317');
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: true,
      apiBase: 'http://manager.local:18317',
      managementKey: '',
      sessionMode: 'manager_embedded',
      connectionStatus: 'connected',
    });
    expect(useUsageServiceStore.getState()).toMatchObject({
      enabled: true,
      serviceBase: 'http://manager.local:18317',
      panelBase: 'http://manager.local:18317',
      panelHostMode: 'manager_embedded',
    });
    expect(storage.getItem('isLoggedIn')).toBeNull();
    expect(apiClientSetConfig).toHaveBeenCalledWith({
      apiBase: 'http://manager.local:18317',
      managementKey: '',
      withCredentials: true,
    });
    expect(clearConfigCacheMock).toHaveBeenCalled();
  });

  it('does not recover regular CPA panel logins through Manager Server config', async () => {
    fetchConfigMock.mockRejectedValue(new Error('invalid management key'));

    const { useAuthStore } = await import('./useAuthStore');

    await expect(
      useAuthStore.getState().login({
        apiBase: 'http://cpa.local:8317',
        managementKey: 'bad-cpa-key',
        sessionMode: 'external_panel',
      })
    ).rejects.toThrow('invalid management key');

    expect(usageServiceGetManagerConfigMock).not.toHaveBeenCalled();
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: false,
      connectionStatus: 'error',
    });
  });

  it('restores a Manager session from the HttpOnly cookie without a stored key', async () => {
    fetchConfigMock.mockResolvedValue(undefined);
    usageServiceGetSessionMock.mockResolvedValue({
      authenticated: true,
      user: { id: 1, email: 'admin@example.com' },
      expiresAt: Date.now() + 60_000,
    });
    const { useAuthStore } = await import('./useAuthStore');
    useAuthStore.setState({
      apiBase: 'http://manager.local:18317',
      managementKey: '',
      sessionMode: 'manager_embedded',
      sessionPanelBase: 'http://manager.local:18317',
    });

    const result = await useAuthStore.getState().restoreSession({
      expectedMode: 'manager_embedded',
      expectedPanelBase: 'http://manager.local:18317',
    });

    expect(result).toEqual({});
    expect(usageServiceGetSessionMock).toHaveBeenCalledWith('http://manager.local:18317');
    expect(usageServiceLoginMock).not.toHaveBeenCalled();
    expect(useAuthStore.getState()).toMatchObject({
      isAuthenticated: true,
      managementKey: '',
      sessionMode: 'manager_embedded',
    });
  });
});
