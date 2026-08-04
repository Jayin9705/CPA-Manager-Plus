/**
 * 认证状态管理
 * 从原项目 src/modules/login.js 和 src/core/connection.js 迁移
 */

import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';
import type {
  AuthSessionMode,
  AuthState,
  LoginCredentials,
  LoginResult,
  RestoreSessionResult,
  ConnectionStatus,
} from '@/types';
import { STORAGE_KEY_AUTH } from '@/utils/constants';
import { obfuscatedStorage } from '@/services/storage/secureStorage';
import { apiClient } from '@/services/api/client';
import { usageServiceApi } from '@/services/api/usageService';
import { useConfigStore } from './useConfigStore';
import { useModelsStore } from './useModelsStore';
import { useQuotaStore } from './useQuotaStore';
import { useUsageServiceStore } from './useUsageServiceStore';
import { detectApiBaseFromLocation, normalizeApiBase } from '@/utils/connection';
import { sha256Hex } from '@/utils/apiKeyHash';
import { normalizeServerMetadataValue } from '@/utils/serverMetadata';
import { clearCsrfToken } from '@/services/security/csrf';

interface AuthStoreState extends AuthState {
  sessionMode: AuthSessionMode | '';
  sessionPanelBase: string;
  connectionStatus: ConnectionStatus;
  connectionError: string | null;

  // 操作
  login: (credentials: LoginCredentials) => Promise<LoginResult>;
  logout: () => Promise<void>;
  checkAuth: () => Promise<boolean>;
  restoreSession: (options?: RestoreSessionOptions) => Promise<RestoreSessionResult>;
  updateServerVersion: (version: string | null, buildDate?: string | null) => void;
  updateServerPluginSupport: (supportsPlugin: boolean) => void;
  updateConnectionStatus: (status: ConnectionStatus, error?: string | null) => void;
}

interface RestoreSessionOptions {
  expectedMode?: AuthSessionMode;
  expectedPanelBase?: string;
}

let restoreSessionPromise: Promise<RestoreSessionResult> | null = null;

const sessionMatchesExpectedRuntime = ({
  expectedMode,
  expectedPanelBase,
  resolvedBase,
  sessionMode,
}: {
  expectedMode?: AuthSessionMode;
  expectedPanelBase?: string;
  resolvedBase: string;
  sessionMode: AuthSessionMode | '';
}) => {
  const normalizedExpectedPanelBase = normalizeApiBase(expectedPanelBase || '');
  if (!expectedMode) return true;
  if (sessionMode && sessionMode !== expectedMode) return false;
  if (expectedMode === 'manager_embedded' && normalizedExpectedPanelBase) {
    return resolvedBase === normalizedExpectedPanelBase;
  }
  if (expectedMode === 'external_panel' && normalizedExpectedPanelBase) {
    return resolvedBase === normalizedExpectedPanelBase;
  }
  return true;
};

const configureApiClient = (
  apiBase: string,
  sessionMode: AuthSessionMode | '',
  managementKey: string
) => {
  const managerSession = sessionMode === 'manager_embedded';
  apiClient.setConfig({
    apiBase,
    managementKey: managerSession ? '' : managementKey,
    withCredentials: managerSession,
  });
};

const validateAuthenticatedConnection = async ({
  apiBase,
  sessionMode,
  sessionPanelBase,
}: {
  apiBase: string;
  sessionMode: AuthSessionMode | '';
  sessionPanelBase: string;
}): Promise<LoginResult> => {
  try {
    await useConfigStore.getState().fetchConfig(undefined, true);
    return {};
  } catch (error) {
    if (sessionMode !== 'manager_embedded') throw error;

    await usageServiceApi.getManagerConfig(apiBase);
    useConfigStore.getState().clearCache();
    useUsageServiceStore.getState().setUsageServiceConfig(
      {
        enabled: true,
        serviceBase: apiBase,
      },
      {
        panelBase: sessionPanelBase || apiBase,
        panelHostMode: 'manager_embedded',
      }
    );
    return { recoveryMode: 'manager_config' };
  }
};

export const useAuthStore = create<AuthStoreState>()(
  persist(
    (set, get) => ({
      // 初始状态
      isAuthenticated: false,
      apiBase: '',
      managementKey: '',
      rememberPassword: false,
      serverVersion: null,
      serverBuildDate: null,
      supportsPlugin: false,
      sessionMode: '',
      sessionPanelBase: '',
      connectionStatus: 'disconnected',
      connectionError: null,

      // 恢复会话并自动登录
      restoreSession: (options) => {
        if (restoreSessionPromise) return restoreSessionPromise;

        restoreSessionPromise = (async () => {
          obfuscatedStorage.migratePlaintextKeys(['apiBase', 'apiUrl', 'managementKey']);

          const wasLoggedIn = localStorage.getItem('isLoggedIn') === 'true';
          const legacyBase =
            obfuscatedStorage.getItem<string>('apiBase') ||
            obfuscatedStorage.getItem<string>('apiUrl', { encrypt: true });
          const legacyKey = obfuscatedStorage.getItem<string>('managementKey');

          const current = get();
          const expectedPanelBase = normalizeApiBase(options?.expectedPanelBase || '');
          const resolvedMode = options?.expectedMode ?? current.sessionMode;
          let resolvedBase = normalizeApiBase(
            resolvedMode === 'manager_embedded' && expectedPanelBase
              ? expectedPanelBase
              : current.apiBase || legacyBase || expectedPanelBase || detectApiBaseFromLocation()
          );
          let resolvedKey =
            resolvedMode === 'manager_embedded'
              ? ''
              : current.managementKey || legacyKey || '';
          let resolvedRememberPassword =
            resolvedMode === 'manager_embedded'
              ? current.rememberPassword
              : current.rememberPassword || Boolean(current.managementKey) || Boolean(legacyKey);

          if (
            !sessionMatchesExpectedRuntime({
              expectedMode: options?.expectedMode,
              expectedPanelBase: options?.expectedPanelBase,
              resolvedBase,
              sessionMode: current.sessionMode,
            })
          ) {
            const fallbackBase = normalizeApiBase(options?.expectedPanelBase || detectApiBaseFromLocation());
            resolvedBase = fallbackBase;
            resolvedKey = '';
            resolvedRememberPassword = false;
            set({
              apiBase: fallbackBase,
              managementKey: '',
              rememberPassword: false,
              sessionMode: options?.expectedMode ?? '',
              sessionPanelBase: normalizeApiBase(options?.expectedPanelBase || ''),
            });
            configureApiClient(fallbackBase, options?.expectedMode ?? '', '');
            localStorage.removeItem('isLoggedIn');
            if (options?.expectedMode !== 'manager_embedded') return false;
          }

          set({
            apiBase: resolvedBase,
            managementKey: resolvedKey,
            rememberPassword: resolvedRememberPassword,
            sessionMode: resolvedMode,
            sessionPanelBase: normalizeApiBase(options?.expectedPanelBase || get().sessionPanelBase)
          });
          configureApiClient(resolvedBase, resolvedMode, resolvedKey);

          if (resolvedMode === 'manager_embedded' && resolvedBase) {
            try {
              await usageServiceApi.getSession(resolvedBase);
              const sessionPanelBase = normalizeApiBase(
                options?.expectedPanelBase || get().sessionPanelBase || resolvedBase
              );
              const result = await validateAuthenticatedConnection({
                apiBase: resolvedBase,
                sessionMode: resolvedMode,
                sessionPanelBase,
              });
              useQuotaStore
                .getState()
                .activateQuotaCacheScope(sha256Hex(`${resolvedBase}\u0000manager-session`));
              useUsageServiceStore.getState().setUsageServiceConfig(
                { enabled: true, serviceBase: resolvedBase },
                { panelBase: sessionPanelBase, panelHostMode: 'manager_embedded' }
              );
              set({
                isAuthenticated: true,
                apiBase: resolvedBase,
                managementKey: '',
                sessionMode: resolvedMode,
                sessionPanelBase,
                connectionStatus: 'connected',
                connectionError: null,
              });
              localStorage.removeItem('isLoggedIn');
              return result.recoveryMode ? result : {};
            } catch (error) {
              console.warn('Manager session restore failed:', error);
              set({ isAuthenticated: false, connectionStatus: 'disconnected' });
              return false;
            }
          }

          if (wasLoggedIn && resolvedBase && resolvedKey) {
            try {
              const result = await get().login({
                apiBase: resolvedBase,
                managementKey: resolvedKey,
                rememberPassword: resolvedRememberPassword,
                sessionMode: resolvedMode || undefined,
                sessionPanelBase: options?.expectedPanelBase || get().sessionPanelBase,
              });
              return result.recoveryMode ? result : {};
            } catch (error) {
              console.warn('Auto login failed:', error);
              return false;
            }
          }

          return false;
        })();

        return restoreSessionPromise;
      },

      // 登录
      login: async (credentials) => {
        const apiBase = normalizeApiBase(credentials.apiBase);
        const rememberPassword = credentials.rememberPassword ?? get().rememberPassword ?? false;
        const sessionMode = credentials.sessionMode ?? get().sessionMode;
        const sessionPanelBase = normalizeApiBase(credentials.sessionPanelBase || get().sessionPanelBase);
        const managerSession = sessionMode === 'manager_embedded';
        const managementKey = managerSession ? '' : (credentials.managementKey || '').trim();
        const quotaCacheScope = sha256Hex(
          `${apiBase}\u0000${managerSession ? 'manager-session' : managementKey}`
        );

        const markAuthenticated = (result: LoginResult = {}) => {
          useQuotaStore.getState().activateQuotaCacheScope(quotaCacheScope);
          configureApiClient(apiBase, sessionMode, managementKey);
          set({
            isAuthenticated: true,
            apiBase,
            managementKey,
            rememberPassword,
            sessionMode,
            sessionPanelBase,
            connectionStatus: 'connected',
            connectionError: null
          });
          if (!managerSession && rememberPassword) {
            localStorage.setItem('isLoggedIn', 'true');
          } else {
            localStorage.removeItem('isLoggedIn');
          }
          return result;
        };

        try {
          set({ connectionStatus: 'connecting', supportsPlugin: false });
          set({ serverVersion: null, serverBuildDate: null });
          useModelsStore.getState().clearCache();

          if (managerSession) {
            const email = (credentials.email || '').trim();
            const password = credentials.password || '';
            if (!credentials.managerSessionEstablished) {
              if (!email || !password) throw new Error('Email and password are required');
              await usageServiceApi.login(apiBase, {
                email,
                password,
                remember: rememberPassword,
              });
            }
            useUsageServiceStore.getState().setUsageServiceConfig(
              { enabled: true, serviceBase: apiBase },
              { panelBase: sessionPanelBase || apiBase, panelHostMode: 'manager_embedded' }
            );
          } else if (!managementKey) {
            throw new Error('Management key is required');
          } else {
            // A bearer-key lightweight panel must never reuse a CSRF token
            // derived from a different Manager Server session.
            clearCsrfToken();
          }

          configureApiClient(apiBase, sessionMode, managementKey);
          const result = await validateAuthenticatedConnection({
            apiBase,
            sessionMode,
            sessionPanelBase,
          });
          return markAuthenticated(result);
        } catch (error: unknown) {
          const message =
            error instanceof Error
              ? error.message
              : typeof error === 'string'
                ? error
                : 'Connection failed';
          set({
            connectionStatus: 'error',
            connectionError: message || 'Connection failed',
            supportsPlugin: false
          });
          if (managerSession && !credentials.managerSessionEstablished) {
            void usageServiceApi.logout(apiBase).catch(() => undefined);
          }
          throw error;
        }
      },

      // 登出
      logout: async () => {
        const { apiBase, sessionMode } = get();
        restoreSessionPromise = null;
        try {
          // Wait for the HttpOnly Manager session to be revoked before the
          // login route is rendered. Otherwise its startup restore can race
          // this request and immediately authenticate the user again.
          if (sessionMode === 'manager_embedded' && apiBase) {
            await usageServiceApi.logout(apiBase);
          }
        } catch {
          // Local logout must still complete when the server is unavailable.
        } finally {
          useConfigStore.getState().clearCache();
          useModelsStore.getState().clearCache();
          useQuotaStore.getState().clearQuotaCache();
          useUsageServiceStore.getState().clearUsageServiceConfig();
          clearCsrfToken();
          configureApiClient('', '', '');
          set({
            isAuthenticated: false,
            apiBase: '',
            managementKey: '',
            rememberPassword: false,
            serverVersion: null,
            serverBuildDate: null,
            supportsPlugin: false,
            sessionMode: '',
            sessionPanelBase: '',
            connectionStatus: 'disconnected',
            connectionError: null
          });
          localStorage.removeItem('isLoggedIn');
        }
      },

      // 检查认证状态
      checkAuth: async () => {
        const { managementKey, apiBase, sessionMode, sessionPanelBase } = get();

        if (!apiBase || (sessionMode !== 'manager_embedded' && !managementKey)) {
          return false;
        }

        try {
          configureApiClient(apiBase, sessionMode, managementKey);
          set({ supportsPlugin: false });

          if (sessionMode === 'manager_embedded') {
            await usageServiceApi.getSession(apiBase);
          }
          await validateAuthenticatedConnection({
            apiBase,
            sessionMode,
            sessionPanelBase,
          });

          set({
            isAuthenticated: true,
            connectionStatus: 'connected'
          });

          return true;
        } catch {
          set({
            isAuthenticated: false,
            connectionStatus: 'error',
            supportsPlugin: false
          });
          return false;
        }
      },

      // 更新服务器版本
      updateServerVersion: (version, buildDate) => {
        const normalizedVersion = normalizeServerMetadataValue(version);
        const normalizedBuildDate = normalizeServerMetadataValue(buildDate);
        set((state) => ({
          // A response may contain only one metadata header. Keep the other
          // value from the same connection instead of erasing it.
          serverVersion: normalizedVersion ?? state.serverVersion,
          serverBuildDate: normalizedBuildDate ?? state.serverBuildDate,
        }));
      },

      updateServerPluginSupport: (supportsPlugin) => {
        set({ supportsPlugin });
      },

      // 更新连接状态
      updateConnectionStatus: (status, error = null) => {
        set({
          connectionStatus: status,
          connectionError: error
        });
      }
    }),
    {
      name: STORAGE_KEY_AUTH,
      storage: createJSONStorage(() => ({
        getItem: (name) => {
          const data = obfuscatedStorage.getItem<AuthStoreState>(name);
          return data ? JSON.stringify(data) : null;
        },
        setItem: (name, value) => {
          obfuscatedStorage.setItem(name, JSON.parse(value));
        },
        removeItem: (name) => {
          obfuscatedStorage.removeItem(name);
        }
      })),
      partialize: (state) => ({
        apiBase: state.apiBase,
        ...(state.sessionMode !== 'manager_embedded' && state.rememberPassword
          ? { managementKey: state.managementKey }
          : {}),
        rememberPassword: state.rememberPassword,
        serverVersion: state.serverVersion,
        serverBuildDate: state.serverBuildDate,
        sessionMode: state.sessionMode,
        sessionPanelBase: state.sessionPanelBase
      })
    }
  )
);

// 监听全局未授权事件
if (typeof window !== 'undefined') {
  window.addEventListener('unauthorized', () => {
    void useAuthStore.getState().logout();
  });

  window.addEventListener(
    'server-version-update',
    ((e: CustomEvent) => {
      const detail = e.detail || {};
      useAuthStore.getState().updateServerVersion(detail.version || null, detail.buildDate || null);
    }) as EventListener
  );

  window.addEventListener(
    'server-plugin-support-update',
    ((e: CustomEvent) => {
      useAuthStore.getState().updateServerPluginSupport(e.detail?.supportsPlugin === true);
    }) as EventListener
  );
}
