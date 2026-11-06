const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { collectTranslationReferences } = require('./helpers/i18n-references.cjs');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-i18n-'));
try {
  fs.symlinkSync(path.join(root, 'node_modules'), path.join(temporary, 'node_modules'), 'dir');
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/i18n/languages.ts',
      'src/i18n/formatters.ts',
      'src/i18n/resources.ts',
      'src/i18n/qaMessages.ts',
      'src/features/usage/tokenRanking.ts',
      '--rootDir',
      'src',
      '--outDir',
      temporary,
      '--module',
      'commonjs',
      '--target',
      'es2020',
      '--skipLibCheck',
      '--strict',
      '--esModuleInterop',
    ],
    { cwd: root, stdio: 'pipe' },
  );
} catch (error) {
  fs.rmSync(temporary, { recursive: true, force: true });
  throw error;
}
after(() => fs.rmSync(temporary, { recursive: true, force: true }));
const { LANGUAGE_OPTIONS, isSupportedLanguage, getPreferredLanguage } = require(
  path.join(temporary, 'i18n/languages.js'),
);
const { loadI18nResources } = require(path.join(temporary, 'i18n/resources.js'));
const { qaChinese, qaEnglish } = require(path.join(temporary, 'i18n/qaMessages.js'));
const {
  formatNumber,
  formatCurrency,
  formatPercent,
  formatDateTime,
  TIMEZONE_OPTIONS,
  getTimezoneLabel,
} = require(path.join(temporary, 'i18n/formatters.js'));
const { formatUsageCount } = require(path.join(temporary, 'features/usage/tokenRanking.js'));

function flatten(value, prefix = '', result = {}) {
  for (const [key, item] of Object.entries(value)) {
    if (typeof item === 'string') result[prefix + key] = item;
    else flatten(item, `${prefix}${key}.`, result);
  }
  return result;
}
function placeholders(value) {
  return (value.match(/\{\w+\}/g) || []).sort();
}

test('all five languages load matching UI, calendar and dayjs resources', async () => {
  assert.deepEqual(
    LANGUAGE_OPTIONS.map(option => option.value),
    ['zh-CN', 'en-US', 'ja-JP', 'de-DE', 'es-ES'],
  );
  const expected = {
    'zh-CN': ['zh-cn', 'zh_CN'],
    'en-US': ['en', 'en_US'],
    'ja-JP': ['ja', 'ja_JP'],
    'de-DE': ['de', 'de_DE'],
    'es-ES': ['es', 'es_ES'],
  };
  const dayjs = require('dayjs');
  for (const option of LANGUAGE_OPTIONS) {
    const pending = loadI18nResources(option.value);
    assert.equal(loadI18nResources(option.value), pending);
    const resources = await pending;
    const [dateLocale, calendarLocale] = expected[option.value];
    assert.equal(resources.antdLocale.DatePicker.lang.locale, calendarLocale);
    assert.equal(dayjs('2026-10-29').locale(dateLocale).locale(), dateLocale);
    assert.ok(resources.messages.nav.language);
  }
});

test('all catalogs retain every English key and interpolation placeholder', async () => {
  const english = flatten((await loadI18nResources('en-US')).messages);
  for (const language of ['zh-CN', 'ja-JP', 'de-DE', 'es-ES']) {
    const messages = flatten((await loadI18nResources(language)).messages);
    assert.deepEqual(Object.keys(messages).sort(), Object.keys(english).sort(), language);
    for (const [key, value] of Object.entries(messages)) {
      assert.ok(value.trim(), `${language}: ${key} is empty`);
      assert.deepEqual(placeholders(value), placeholders(english[key]), `${language}: ${key}`);
    }
    for (const key of [
      'nav.language',
      'ux.hint_timeout',
      'monitor.traffic_note',
      'auth.mfa_disable_heading',
    ]) {
      assert.notEqual(messages[key], english[key], `${language}: ${key}`);
    }
  }
});

test('UI translation references resolve in every loaded catalog, including fallback calls', async () => {
  const english = (await loadI18nResources('en-US')).messages;
  const references = collectTranslationReferences(root, new Set(Object.keys(english)));
  assert.ok(references.size > 0);
  for (const { value: language } of LANGUAGE_OPTIONS) {
    const messages = flatten((await loadI18nResources(language)).messages);
    const missing = [...references]
      .filter(([key]) => !messages[key]?.trim())
      .map(([key, location]) => `${key} (${location})`);
    assert.deepEqual(missing, [], `${language}: unresolved UI translations`);
  }
});

test('loaded catalogs include QA messages and language selector labels', async () => {
  for (const { value: language } of LANGUAGE_OPTIONS) {
    const messages = flatten((await loadI18nResources(language)).messages);
    const expected = language === 'zh-CN' ? qaChinese : qaEnglish;
    for (const [key, value] of Object.entries(expected)) {
      assert.equal(messages[`qa.${key}`], value, `${language}: qa.${key}`);
    }
    for (const { labelKey } of LANGUAGE_OPTIONS) {
      assert.ok(messages[labelKey]?.trim(), `${language}: ${labelKey}`);
      assert.notEqual(messages[labelKey], labelKey);
    }
  }
});

test('stored preferences win and browser regional variants map to supported languages', () => {
  for (const { value } of LANGUAGE_OPTIONS) {
    assert.ok(isSupportedLanguage(value));
    assert.equal(getPreferredLanguage(value, ['en-GB']), value);
  }
  for (const [browser, expected] of [
    ['ja', 'ja-JP'],
    ['de-AT', 'de-DE'],
    ['es-MX', 'es-ES'],
    ['en-GB', 'en-US'],
    ['zh-Hans', 'zh-CN'],
  ]) {
    assert.equal(getPreferredLanguage('invalid', ['fr-FR', browser]), expected);
  }
  assert.equal(getPreferredLanguage(null, ['fr-FR', 'ja-JP', 'de-DE']), 'ja-JP');
  assert.equal(getPreferredLanguage(null, ['fr-FR']), 'en-US');
  assert.equal(getPreferredLanguage(undefined, []), 'en-US');
  assert.equal(isSupportedLanguage('fr-FR'), false);
});

test('numbers, currency, percentages and compact token counts follow the selected locale', () => {
  for (const { value: locale } of LANGUAGE_OPTIONS) {
    assert.equal(
      formatNumber(12345.67, locale, 2),
      new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(
        12345.67,
      ),
    );
    assert.equal(
      formatCurrency(1234.5, locale),
      new Intl.NumberFormat(locale, {
        style: 'currency',
        currency: 'USD',
        currencyDisplay: 'narrowSymbol',
        minimumFractionDigits: 2,
        maximumFractionDigits: 4,
      }).format(1234.5),
    );
    assert.equal(
      formatPercent(12.5, 1, locale),
      new Intl.NumberFormat(locale, {
        style: 'percent',
        minimumFractionDigits: 1,
        maximumFractionDigits: 1,
      }).format(0.125),
    );
  }
  assert.equal(formatNumber(12345.67, 'de-DE', 2), '12.345,67');
  assert.equal(formatNumber(12345.67, 'es-ES', 2), '12.345,67');
  assert.equal(formatNumber(12345.67, 'ja-JP', 2), '12,345.67');
  for (const locale of ['ja-JP', 'de-DE', 'es-ES']) {
    assert.equal(
      formatUsageCount(12345678, locale),
      new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 2 }).format(
        12345678,
      ),
    );
  }
  assert.equal(formatUsageCount(10000, 'zh-CN'), '1 万');
  assert.equal(formatUsageCount(10000, 'en-US'), '10 K');
});

test('timezone label export supports every locale and preserves the selected timezone', () => {
  for (const { value: language } of LANGUAGE_OPTIONS) {
    for (const option of TIMEZONE_OPTIONS) assert.ok(getTimezoneLabel(option, language).trim());
    assert.equal(
      formatDateTime('2026-10-29T00:00:00Z', 'Asia/Tokyo', language),
      '2026-10-29 09:00:00',
    );
    assert.equal(formatDateTime('2026-10-29T00:00:00Z', 'UTC', language), '2026-10-29 00:00:00');
  }
  assert.equal(getTimezoneLabel(TIMEZONE_OPTIONS[0], 'de-DE'), 'Lokale Browserzeitzone');
  assert.equal(getTimezoneLabel(TIMEZONE_OPTIONS[0], 'ja-JP'), 'ブラウザーのタイムゾーン');
});
