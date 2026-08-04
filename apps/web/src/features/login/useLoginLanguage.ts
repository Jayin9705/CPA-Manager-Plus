import { useCallback, useState } from 'react';
import type { Language } from '@/types';
import { STORAGE_KEY_LANGUAGE } from '@/utils/constants';
import { getInitialLanguage, isSupportedLanguage } from '@/utils/language';
import { loginResources } from '@/i18n/loginResources';

type TranslationOptions = Record<string, unknown>;

const readValue = (resource: unknown, key: string): unknown => {
  return key.split('.').reduce<unknown>((current, segment) => {
    if (!current || typeof current !== 'object' || Array.isArray(current)) return undefined;
    return (current as Record<string, unknown>)[segment];
  }, resource);
};

const interpolate = (value: string, options?: TranslationOptions) =>
  value.replace(/{{\s*([^}\s]+)\s*}}/g, (_match, name: string) => {
    const replacement = options?.[name];
    return replacement === undefined ? `{{${name}}}` : String(replacement);
  });

const persistLanguage = (language: Language) => {
  try {
    localStorage.setItem(STORAGE_KEY_LANGUAGE, JSON.stringify({ state: { language }, version: 0 }));
  } catch {
    // Storage may be unavailable in private browsing.
  }
};

export function useLoginLanguage() {
  const [language, setLanguageState] = useState<Language>(getInitialLanguage);

  const setLanguage = useCallback((nextLanguage: string) => {
    if (!isSupportedLanguage(nextLanguage)) return;
    setLanguageState(nextLanguage);
    persistLanguage(nextLanguage);
  }, []);

  const t = useCallback(
    (key: string, options?: TranslationOptions) => {
      const currentResource = loginResources[language]?.translation;
      const fallbackResource = loginResources['zh-CN'].translation;
      const value = readValue(currentResource, key) ?? readValue(fallbackResource, key);
      const fallback = options?.defaultValue;
      return interpolate(
        typeof value === 'string' ? value : typeof fallback === 'string' ? fallback : key,
        options
      );
    },
    [language]
  );

  return { language, setLanguage, t };
}
