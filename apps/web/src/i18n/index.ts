/**
 * i18next 国际化配置
 */

import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import type { Language } from '@/types';
import { getInitialLanguage } from '@/utils/language';
import { loginResources } from './loginResources';

// Runtime tests use the complete default locale synchronously. Production starts
// with the small login dictionary and loads the full locale after authentication.
import fullZhCNForTests from './locales/zh-CN.json';

type TranslationResource = Record<string, unknown>;

const localeLoaders: Partial<
  Record<Language, () => Promise<{ default: TranslationResource }>>
> = {
  'zh-CN': () => import('./locales/zh-CN.json'),
  'zh-TW': () => import('./locales/zh-TW.json'),
  en: () => import('./locales/en.json'),
  ru: () => import('./locales/ru.json')
};

const pendingLocaleLoads = new Map<Language, Promise<void>>();
const fullLocalesLoaded = new Set<Language>(
  import.meta.env.MODE === 'test' ? ['zh-CN'] : []
);

const initialLanguage = getInitialLanguage();
const initialResources =
  import.meta.env.MODE === 'test'
    ? { 'zh-CN': { translation: fullZhCNForTests } }
    : loginResources;

i18n.use(initReactI18next).init({
  resources: initialResources,
  lng: initialLanguage,
  fallbackLng: 'zh-CN',
  interpolation: {
    escapeValue: false // React 已经转义
  },
  react: {
    useSuspense: false
  }
});

export const ensureLanguageLoaded = async (language: Language): Promise<void> => {
  if (fullLocalesLoaded.has(language)) {
    return;
  }

  const existingRequest = pendingLocaleLoads.get(language);
  if (existingRequest) {
    return existingRequest;
  }

  const loader = localeLoaders[language];
  if (!loader) {
    return;
  }

  const request = loader().then(({ default: resource }) => {
    i18n.addResourceBundle(language, 'translation', resource, true, true);
    fullLocalesLoaded.add(language);
  });
  pendingLocaleLoads.set(language, request);

  try {
    await request;
  } finally {
    pendingLocaleLoads.delete(language);
  }
};

export default i18n;
