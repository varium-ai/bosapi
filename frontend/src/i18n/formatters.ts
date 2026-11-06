import {
  type SupportedLanguage,
  type SupportedTimezone,
  type TimezoneOption,
  type FormatDateTimeOptions,
  type FormatCurrencyOptions,
} from './types';

export const TIMEZONE_OPTIONS: TimezoneOption[] = [
  { value: 'local', labelEn: 'Browser Local', labelZh: '本地时区 (浏览器)', offset: 'Local' },
  {
    value: 'UTC',
    labelEn: 'UTC (Coordinated Universal Time)',
    labelZh: 'UTC (协调世界时)',
    offset: 'UTC+0',
  },
  {
    value: 'Asia/Shanghai',
    labelEn: 'Beijing / Shanghai (CST)',
    labelZh: '北京 / 上海时间 (UTC+8)',
    offset: 'UTC+8',
  },
  {
    value: 'Asia/Singapore',
    labelEn: 'Singapore (SGT)',
    labelZh: '新加坡时间 (UTC+8)',
    offset: 'UTC+8',
  },
  { value: 'Asia/Tokyo', labelEn: 'Tokyo (JST)', labelZh: '东京时间 (UTC+9)', offset: 'UTC+9' },
  {
    value: 'Europe/London',
    labelEn: 'London (GMT/BST)',
    labelZh: '伦敦时间 (UTC+0/+1)',
    offset: 'UTC+0',
  },
  {
    value: 'America/New_York',
    labelEn: 'New York (EST/EDT)',
    labelZh: '纽约时间 (UTC-5/-4)',
    offset: 'UTC-5',
  },
  {
    value: 'America/Los_Angeles',
    labelEn: 'Los Angeles (PST/PDT)',
    labelZh: '洛杉矶时间 (UTC-8/-7)',
    offset: 'UTC-8',
  },
];

const timezoneLabels = {
  'ja-JP': {
    local: 'ブラウザーのタイムゾーン',
    UTC: 'UTC（協定世界時）',
    'Asia/Shanghai': '北京 / 上海（UTC+8）',
    'Asia/Tokyo': '東京（UTC+9）',
    'Asia/Singapore': 'シンガポール（UTC+8）',
    'Europe/London': 'ロンドン（UTC+0/+1）',
    'America/New_York': 'ニューヨーク（UTC-5/-4）',
    'America/Los_Angeles': 'ロサンゼルス（UTC-8/-7）',
  },
  'de-DE': {
    local: 'Lokale Browserzeitzone',
    UTC: 'UTC (Koordinierte Weltzeit)',
    'Asia/Shanghai': 'Peking / Shanghai (UTC+8)',
    'Asia/Tokyo': 'Tokio (UTC+9)',
    'Asia/Singapore': 'Singapur (UTC+8)',
    'Europe/London': 'London (UTC+0/+1)',
    'America/New_York': 'New York (UTC-5/-4)',
    'America/Los_Angeles': 'Los Angeles (UTC-8/-7)',
  },
  'es-ES': {
    local: 'Zona horaria del navegador',
    UTC: 'UTC (tiempo universal coordinado)',
    'Asia/Shanghai': 'Pekín / Shanghái (UTC+8)',
    'Asia/Tokyo': 'Tokio (UTC+9)',
    'Asia/Singapore': 'Singapur (UTC+8)',
    'Europe/London': 'Londres (UTC+0/+1)',
    'America/New_York': 'Nueva York (UTC-5/-4)',
    'America/Los_Angeles': 'Los Ángeles (UTC-8/-7)',
  },
} satisfies Record<
  Exclude<SupportedLanguage, 'zh-CN' | 'en-US'>,
  Record<SupportedTimezone, string>
>;

export function getTimezoneLabel(option: TimezoneOption, language: SupportedLanguage): string {
  if (language === 'zh-CN') return option.labelZh;
  if (language === 'en-US') return option.labelEn;
  return timezoneLabels[language][option.value];
}

/** Resolves browser-local preferences to an IANA timezone accepted by the server. */
export function resolveTimezone(timezone: SupportedTimezone): string {
  return timezone === 'local'
    ? Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
    : timezone;
}

/**
 * Returns a short readable badge for the given timezone, e.g. 'UTC', 'UTC+8', 'Local (UTC+8)'
 */
export function getTimezoneBadge(timezone: SupportedTimezone): string {
  if (!timezone || timezone === 'local') {
    const offsetMin = -new Date().getTimezoneOffset();
    const sign = offsetMin >= 0 ? '+' : '-';
    const hours = Math.floor(Math.abs(offsetMin) / 60);
    return `UTC${sign}${hours}`;
  }
  if (timezone === 'UTC') return 'UTC';
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      timeZoneName: 'short',
    }).formatToParts(new Date());
    const tzPart = parts.find(p => p.type === 'timeZoneName');
    return tzPart ? tzPart.value : timezone;
  } catch {
    return timezone;
  }
}

/**
 * Formats a Date/timestamp into unified YYYY-MM-DD HH:mm:ss format under the specified timezone
 */
export function formatDateTime(
  input: string | number | Date | null | undefined,
  timezone: SupportedTimezone = 'local',
  locale: SupportedLanguage = 'zh-CN',
  options: FormatDateTimeOptions = {},
): string {
  if (!input) return '-';
  const d = input instanceof Date ? input : new Date(input);
  if (isNaN(d.getTime())) return String(input);

  const {
    showTimezone = false,
    includeSeconds = true,
    dateOnly = false,
    timeOnly = false,
  } = options;

  const intlOpts: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  };

  if (includeSeconds) {
    intlOpts.second = '2-digit';
  }

  if (timezone && timezone !== 'local') {
    intlOpts.timeZone = timezone;
  }

  try {
    const parts = new Intl.DateTimeFormat('en-GB', intlOpts).formatToParts(d);
    const map: Record<string, string> = {};
    for (const p of parts) {
      map[p.type] = p.value;
    }

    const dateStr = `${map.year}-${map.month}-${map.day}`;
    const timeStr = `${map.hour}:${map.minute}${includeSeconds ? `:${map.second}` : ''}`;

    let result = '';
    if (dateOnly) {
      result = dateStr;
    } else if (timeOnly) {
      result = timeStr;
    } else {
      result = `${dateStr} ${timeStr}`;
    }

    if (showTimezone && !dateOnly) {
      result += ` (${getTimezoneBadge(timezone)})`;
    }

    return result;
  } catch {
    return d.toLocaleString(locale);
  }
}

/**
 * Format date only (YYYY-MM-DD) under target timezone
 */
export function formatDate(
  input: string | number | Date | null | undefined,
  timezone: SupportedTimezone = 'local',
): string {
  return formatDateTime(input, timezone, 'zh-CN', { dateOnly: true });
}

/**
 * Format currency with standardized precision and thousands grouping
 */
export function formatCurrency(
  amount: number | string | null | undefined,
  locale: SupportedLanguage = 'zh-CN',
  options: FormatCurrencyOptions = {},
): string {
  const { currency = 'USD', decimals = 4, minDecimals = 2 } = options;

  const num = Number(amount ?? 0);
  if (isNaN(num)) return '-';

  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      currencyDisplay: 'narrowSymbol',
      minimumFractionDigits: minDecimals,
      maximumFractionDigits: decimals,
    }).format(num);
  } catch {
    const sign = currency === 'USD' ? '$' : currency === 'CNY' ? '¥' : `${currency} `;
    return `${sign}${num.toFixed(decimals)}`;
  }
}

/**
 * Format price per 1M tokens with currency symbol
 */
export function formatPricePerMillion(
  price: number | string | null | undefined,
  locale: SupportedLanguage = 'zh-CN',
  currency: string = 'USD',
): string {
  return `${formatCurrency(price, locale, { currency, decimals: 4, minDecimals: 2 })} / 1M`;
}

/**
 * Standard number formatting with thousands separators
 */
export function formatNumber(
  val: number | string | null | undefined,
  locale: SupportedLanguage = 'zh-CN',
  decimals?: number,
): string {
  const num = Number(val ?? 0);
  if (isNaN(num)) return '0';

  const intlOpts: Intl.NumberFormatOptions = {};
  if (typeof decimals === 'number') {
    intlOpts.minimumFractionDigits = decimals;
    intlOpts.maximumFractionDigits = decimals;
  }

  return new Intl.NumberFormat(locale, intlOpts).format(num);
}

/**
 * Format token counts with thousands separators
 */
export function formatTokens(
  tokens: number | string | null | undefined,
  locale: SupportedLanguage = 'zh-CN',
): string {
  return formatNumber(tokens, locale);
}

/**
 * Format percentage
 */
export function formatPercent(
  val: number | string | null | undefined,
  decimals: number = 1,
  locale: SupportedLanguage = 'zh-CN',
): string {
  const num = Number(val ?? 0);
  if (isNaN(num)) return '0%';
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(num / 100);
}
