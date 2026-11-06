import type { SupportedLanguage } from './types';

export const LANGUAGE_OPTIONS = [
  { value: 'zh-CN', labelKey: 'languages.zh-CN', shortLabel: '中文' },
  { value: 'en-US', labelKey: 'languages.en-US', shortLabel: 'EN' },
  { value: 'ja-JP', labelKey: 'languages.ja-JP', shortLabel: '日本語' },
  { value: 'de-DE', labelKey: 'languages.de-DE', shortLabel: 'DE' },
  { value: 'es-ES', labelKey: 'languages.es-ES', shortLabel: 'ES' },
] satisfies { value: SupportedLanguage; labelKey: string; shortLabel: string }[];

export function isSupportedLanguage(value: unknown): value is SupportedLanguage {
  return LANGUAGE_OPTIONS.some(option => option.value === value);
}

export function getPreferredLanguage(
  saved: unknown,
  browserLanguages: readonly string[],
): SupportedLanguage {
  if (isSupportedLanguage(saved)) return saved;
  for (const language of browserLanguages) {
    const base = language.toLowerCase().split(/[-_]/)[0];
    const match = LANGUAGE_OPTIONS.find(option => option.value.split('-')[0] === base);
    if (match) return match.value;
  }
  return 'en-US';
}
