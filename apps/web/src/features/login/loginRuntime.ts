import { obfuscatedStorage } from '@/services/storage/secureStorage';
import { STORAGE_KEY_AUTH } from '@/utils/constants';

interface PersistedAuthState {
  apiBase?: string;
  managementKey?: string;
  rememberPassword?: boolean;
}

export interface StoredLoginState {
  apiBase: string;
  managementKey: string;
  rememberPassword: boolean;
}

export const readStoredLoginState = (): StoredLoginState => {
  try {
    const persisted = obfuscatedStorage.getItem<PersistedAuthState>(STORAGE_KEY_AUTH);
    const legacyBase =
      obfuscatedStorage.getItem<string>('apiBase') ||
      obfuscatedStorage.getItem<string>('apiUrl', { encrypt: true }) ||
      '';
    const legacyKey = obfuscatedStorage.getItem<string>('managementKey') || '';
    const managementKey = persisted?.managementKey || legacyKey;
    return {
      apiBase: persisted?.apiBase || legacyBase,
      managementKey,
      rememberPassword: Boolean(persisted?.rememberPassword || managementKey),
    };
  } catch {
    return { apiBase: '', managementKey: '', rememberPassword: false };
  }
};

export const loadAuthStore = async () => {
  const { useAuthStore } = await import('@/stores/useAuthStore');
  return useAuthStore;
};
