export type SupportedLanguage = 'zh-CN' | 'en-US' | 'ja-JP' | 'de-DE' | 'es-ES';
export type SupportedTimezone =
  | 'local'
  | 'UTC'
  | 'Asia/Shanghai'
  | 'Asia/Tokyo'
  | 'Asia/Singapore'
  | 'Europe/London'
  | 'America/New_York'
  | 'America/Los_Angeles';

export type SupportedCurrency = 'USD' | 'CNY';

export interface TimezoneOption {
  value: SupportedTimezone;
  labelEn: string;
  labelZh: string;
  offset: string;
}

export interface FormatDateTimeOptions {
  showTimezone?: boolean;
  includeSeconds?: boolean;
  dateOnly?: boolean;
  timeOnly?: boolean;
}

export interface FormatCurrencyOptions {
  currency?: string;
  decimals?: number;
  minDecimals?: number;
}
