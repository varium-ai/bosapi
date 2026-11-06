import React, {
  createContext,
  useContext,
  useState,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from 'react';
import type { Locale as AntdLocale } from 'antd/es/locale';
import {
  type SupportedLanguage,
  type SupportedTimezone,
  type SupportedCurrency,
  type FormatDateTimeOptions,
  type FormatCurrencyOptions,
} from './types';
import { loadI18nResources, type I18nResources } from './resources';
import { getPreferredLanguage } from './languages';
import {
  formatDateTime as baseFormatDateTime,
  formatDate as baseFormatDate,
  formatNumber as baseFormatNumber,
  formatTokens as baseFormatTokens,
  formatCurrency as baseFormatCurrency,
  formatPricePerMillion as baseFormatPricePerMillion,
  formatPercent as baseFormatPercent,
  getTimezoneBadge,
  TIMEZONE_OPTIONS,
} from './formatters';

interface I18nContextValue {
  language: SupportedLanguage;
  setLanguage: (lang: SupportedLanguage) => void;
  timezone: SupportedTimezone;
  setTimezone: (tz: SupportedTimezone) => void;
  currency: SupportedCurrency;
  setCurrency: (curr: SupportedCurrency) => void;
  antdLocale: AntdLocale;
  timezoneBadge: string;
  t: (path: string, fallback?: string) => string;
  formatDateTime: (
    val: string | number | Date | null | undefined,
    options?: FormatDateTimeOptions,
  ) => string;
  formatDate: (val: string | number | Date | null | undefined) => string;
  formatNumber: (val: number | string | null | undefined, decimals?: number) => string;
  formatTokens: (tokens: number | string | null | undefined) => string;
  formatCurrency: (
    amount: number | string | null | undefined,
    options?: FormatCurrencyOptions,
  ) => string;
  formatPricePerMillion: (price: number | string | null | undefined, curr?: string) => string;
  formatPercent: (val: number | string | null | undefined, decimals?: number) => string;
}

const STORAGE_LANG_KEY = 'bos_lang';
const STORAGE_TZ_KEY = 'bos_timezone';
const STORAGE_CURRENCY_KEY = 'bos_currency';

const I18nContext = createContext<I18nContextValue | null>(null);

export function getInitialLanguage(): SupportedLanguage {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(STORAGE_LANG_KEY);
  } catch {
    // Browser language detection also works when storage is unavailable.
  }
  return getPreferredLanguage(saved, navigator.languages || [navigator.language]);
}

function getInitialTimezone(): SupportedTimezone {
  try {
    const saved = localStorage.getItem(STORAGE_TZ_KEY) as SupportedTimezone;
    if (saved && TIMEZONE_OPTIONS.some(o => o.value === saved)) {
      return saved;
    }
  } catch {
    // fallback
  }
  return 'local';
}

function getInitialCurrency(): SupportedCurrency {
  try {
    const saved = localStorage.getItem(STORAGE_CURRENCY_KEY) as SupportedCurrency;
    if (saved === 'USD' || saved === 'CNY') {
      return saved;
    }
  } catch {
    // fallback
  }
  return 'USD';
}

export const I18nProvider: React.FC<{
  children: ReactNode;
  initialLanguage: SupportedLanguage;
  initialResources: I18nResources;
}> = ({ children, initialLanguage, initialResources }) => {
  const [language, setLanguageState] = useState<SupportedLanguage>(initialLanguage);
  const [resources, setResources] = useState<I18nResources>(initialResources);
  const [timezone, setTimezoneState] = useState<SupportedTimezone>(getInitialTimezone);
  const [currency, setCurrencyState] = useState<SupportedCurrency>(getInitialCurrency);
  const requestedLanguage = useRef(initialLanguage);

  const setLanguage = (lang: SupportedLanguage) => {
    if (lang === requestedLanguage.current) return;
    requestedLanguage.current = lang;
    void loadI18nResources(lang)
      .then(nextResources => {
        if (requestedLanguage.current !== lang) return;
        setResources(nextResources);
        setLanguageState(lang);
        document.documentElement.lang = lang;
        try {
          localStorage.setItem(STORAGE_LANG_KEY, lang);
        } catch {
          // The selected language still applies when persistence is unavailable.
        }
      })
      .catch(() => {
        if (requestedLanguage.current === lang) requestedLanguage.current = language;
      });
  };

  const setTimezone = (tz: SupportedTimezone) => {
    setTimezoneState(tz);
    try {
      localStorage.setItem(STORAGE_TZ_KEY, tz);
    } catch {
      // Keep the in-memory timezone if browser storage is unavailable.
    }
  };

  const setCurrency = (curr: SupportedCurrency) => {
    setCurrencyState(curr);
    try {
      localStorage.setItem(STORAGE_CURRENCY_KEY, curr);
    } catch {
      // Keep the in-memory currency if browser storage is unavailable.
    }
  };

  useEffect(() => {
    document.documentElement.lang = language;
  }, [language]);

  const antdLocale = resources.antdLocale;

  const timezoneBadge = useMemo(() => {
    return getTimezoneBadge(timezone);
  }, [timezone]);

  const t = useMemo(() => {
    return (path: string, fallback?: string): string => {
      const keys = path.split('.');
      let current = resources.messages;
      for (const k of keys) {
        if (current && typeof current === 'object' && k in current) {
          current = current[k];
        } else {
          return fallback || path;
        }
      }
      return typeof current === 'string' ? current : fallback || path;
    };
  }, [resources]);

  const value: I18nContextValue = useMemo(() => {
    return {
      language,
      setLanguage,
      timezone,
      setTimezone,
      currency,
      setCurrency,
      antdLocale,
      timezoneBadge,
      t,
      formatDateTime: (val, options) => baseFormatDateTime(val, timezone, language, options),
      formatDate: val => baseFormatDate(val, timezone),
      formatNumber: (val, decimals) => baseFormatNumber(val, language, decimals),
      formatTokens: tokens => baseFormatTokens(tokens, language),
      formatCurrency: (amount, options) =>
        baseFormatCurrency(amount, language, { currency, ...options }),
      formatPricePerMillion: (price, curr) =>
        baseFormatPricePerMillion(price, language, curr || currency),
      formatPercent: (val, decimals) => baseFormatPercent(val, decimals, language),
    };
  }, [language, timezone, currency, antdLocale, timezoneBadge, t]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
};

export const useI18n = (): I18nContextValue => {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error('useI18n must be used within an I18nProvider');
  }
  return ctx;
};
