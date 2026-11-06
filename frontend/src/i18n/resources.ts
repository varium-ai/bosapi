import type { Locale as AntdLocale } from 'antd/es/locale';
import type { SupportedLanguage } from './types';

export interface I18nResources {
  messages: Record<string, any>;
  antdLocale: AntdLocale;
}

const loaders: Record<SupportedLanguage, () => Promise<I18nResources>> = {
  'zh-CN': async () => {
    const [{ zhCN }, { default: antdLocale }] = await Promise.all([
      import('./locales/zh-CN'),
      import('antd/locale/zh_CN'),
      import('dayjs/locale/zh-cn'),
    ]);
    return { messages: zhCN, antdLocale };
  },
  'en-US': async () => {
    const [{ enUS }, { default: antdLocale }] = await Promise.all([
      import('./locales/en-US'),
      import('antd/locale/en_US'),
    ]);
    return { messages: enUS, antdLocale };
  },
  'ja-JP': async () => {
    const [{ jaJP }, { default: antdLocale }] = await Promise.all([
      import('./locales/ja-JP'),
      import('antd/locale/ja_JP'),
      import('dayjs/locale/ja'),
    ]);
    return { messages: jaJP, antdLocale };
  },
  'de-DE': async () => {
    const [{ deDE }, { default: antdLocale }] = await Promise.all([
      import('./locales/de-DE'),
      import('antd/locale/de_DE'),
      import('dayjs/locale/de'),
    ]);
    return { messages: deDE, antdLocale };
  },
  'es-ES': async () => {
    const [{ esES }, { default: antdLocale }] = await Promise.all([
      import('./locales/es-ES'),
      import('antd/locale/es_ES'),
      import('dayjs/locale/es'),
    ]);
    return { messages: esES, antdLocale };
  },
};

const cache = new Map<SupportedLanguage, Promise<I18nResources>>();

export function loadI18nResources(language: SupportedLanguage) {
  const cached = cache.get(language);
  if (cached) return cached;
  const pending = loaders[language]();
  cache.set(language, pending);
  void pending.catch(() => {
    if (cache.get(language) === pending) cache.delete(language);
  });
  return pending;
}
