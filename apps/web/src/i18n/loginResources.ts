import type { Language } from '@/types';
import en from './login/en.json';
import ru from './login/ru.json';
import zhCN from './login/zh-CN.json';
import zhTW from './login/zh-TW.json';

type LoginResource = { translation: Record<string, unknown> };

export const loginResources: Record<Language, LoginResource> = {
  'zh-CN': { translation: zhCN },
  'zh-TW': { translation: zhTW },
  en: { translation: en },
  ru: { translation: ru },
};
