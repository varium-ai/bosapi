const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-user-usage-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/usage/userUsageFilters.ts',
      'src/features/usage/tokenRanking.ts',
      'src/features/usage/requestTiming.ts',
      'src/features/usage/usageQueryCache.ts',
      'src/i18n/locales/en-US.ts',
      'src/i18n/locales/zh-CN.ts',
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
    ],
    { cwd: root, stdio: 'pipe' },
  );
} catch (error) {
  fs.rmSync(temporary, { recursive: true, force: true });
  throw error;
}
after(() => fs.rmSync(temporary, { recursive: true, force: true }));
const { businessToday, defaultUsageRange, validUserUsageRange } = require(
  path.join(temporary, 'features/usage/userUsageFilters.js'),
);
const { formatFirstTokenLatency } = require(
  path.join(temporary, 'features/usage/requestTiming.js'),
);
const { refreshUsageQueries, usageRefreshParams } = require(
  path.join(temporary, 'features/usage/usageQueryCache.js'),
);

test('manual usage refresh bypasses server cache while page changes and ordinary fetches reuse it', async () => {
  const { QueryClient, QueryObserver } = require('@tanstack/react-query');
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const requests = [];
  const queryKey = ['admin-organization-daily-usage', { organization_id: 9 }, 1, 20];
  const queryFn = async ({ queryKey }) => {
    requests.push(usageRefreshParams(client, queryKey));
    return { items: [], total: 0 };
  };
  const options = { queryKey, queryFn };
  const observer = new QueryObserver(client, options);
  const unsubscribe = observer.subscribe(() => {});
  try {
    await client.fetchQuery(options);
    await client.fetchQuery(options);
    assert.deepEqual(requests, [{}]);
    const secondPage = [...queryKey.slice(0, 2), 2, 20];
    await client.fetchQuery({ ...options, queryKey: secondPage });
    await refreshUsageQueries(client, ['admin-organization-daily-usage']);
    assert.deepEqual(requests, [{}, {}, { refresh: true }]);
    assert.equal(client.getQueryState(queryKey).isInvalidated, false);
    assert.equal(client.getQueryState(secondPage), undefined);
    await client.fetchQuery({ ...options, staleTime: 0 });
    await client.fetchQuery({ ...options, queryKey: secondPage });
    assert.deepEqual(requests, [{}, {}, { refresh: true }, {}, {}]);
  } finally {
    unsubscribe();
    client.clear();
  }
});

test('first token latency preserves zero and distinguishes unavailable measurements', () => {
  const formatNumber = value => new Intl.NumberFormat('en-US').format(value);
  for (const [value, expected] of [
    [undefined, '-'],
    [null, '-'],
    [0, '0 ms'],
    [42, '42 ms'],
    [1234, '1,234 ms'],
    [NaN, '-'],
    [Infinity, '-'],
    [-1, '-'],
  ]) {
    assert.equal(formatFirstTokenLatency(value, formatNumber), expected, String(value));
  }
});

test('first token latency labels and measurement guidance are available in both languages', () => {
  const english = require(path.join(temporary, 'i18n/locales/en-US.js')).enUS;
  const chinese = require(path.join(temporary, 'i18n/locales/zh-CN.js')).zhCN;
  assert.equal(english.usage.col_first_token_ms, 'Time to First Token');
  assert.equal(chinese.usage.col_first_token_ms, '\u9996\u5b57\u8017\u65f6');
  assert.match(english.usage.first_token_ms_hint, /first model output/);
  assert.match(english.usage.first_token_ms_hint, /headers and heartbeats/);
  assert.match(english.usage.first_token_ms_hint, /non-streaming requests/);
  assert.match(
    chinese.usage.first_token_ms_hint,
    /\u9996\u6b21\u6536\u5230\u6a21\u578b\u8f93\u51fa/,
  );
  assert.match(
    chinese.usage.first_token_ms_hint,
    /\u6d41\u5f0f\u5ffd\u7565\u54cd\u5e94\u5934\u548c\u5fc3\u8df3/,
  );
  assert.match(
    chinese.usage.first_token_ms_hint,
    /\u975e\u6d41\u5f0f\u8bb0\u5f55\u9996\u6bb5\u54cd\u5e94\u4f53\u8017\u65f6/,
  );
});

test('default date follows the UTC business calendar across midnight and years', () => {
  for (const [instant, day] of [
    ['2026-10-15T23:59:59Z', '2026-10-15'],
    ['2026-10-16T00:00:00Z', '2026-10-16'],
    ['2026-12-31T23:59:59Z', '2026-12-31'],
    ['2024-02-29T00:00:00Z', '2024-02-29'],
  ])
    assert.equal(businessToday(new Date(instant)), day);
});

test('daily organization defaults cover only today while other views retain seven UTC dates', () => {
  for (const [instant, from, to] of [
    ['2026-10-22T03:00:00Z', '2026-10-16', '2026-10-22'],
    ['2026-10-22T23:59:59Z', '2026-10-16', '2026-10-22'],
    ['2026-10-23T00:00:00Z', '2026-10-17', '2026-10-23'],
    ['2026-12-31T23:59:59Z', '2026-12-25', '2026-12-31'],
    ['2024-02-29T00:00:00Z', '2024-02-23', '2024-02-29'],
  ]) {
    assert.deepEqual(defaultUsageRange(new Date(instant), 7), { from, to }, instant);
    assert.deepEqual(defaultUsageRange(new Date(instant)), { from: to, to }, instant);
    assert.equal(validUserUsageRange(from, to), true);
  }
});

test('inclusive date ranges allow one day and 366 days and reject invalid dates', () => {
  for (const [from, to] of [
    ['2026-10-16', '2026-10-16'],
    ['2024-01-01', '2024-12-31'],
    ['2024-02-29', '2024-03-01'],
  ])
    assert.equal(validUserUsageRange(from, to), true, `${from}..${to}`);
  for (const [from, to] of [
    ['', '2026-10-16'],
    ['2026-10-16', ''],
    ['2026-10-17', '2026-10-16'],
    ['2024-01-01', '2025-01-01'],
    ['2026-02-30', '2026-03-01'],
    ['2026-13-01', '2026-13-02'],
    ['2026-1-01', '2026-01-02'],
    ["2026-01-01'; DROP TABLE users; --", '2026-01-02'],
  ])
    assert.equal(validUserUsageRange(from, to), false, `${from}..${to}`);
});

test('organization dashboard translations describe ownership, scope, and calendar', () => {
  const english = require(path.join(temporary, 'i18n/locales/en-US.js')).enUS;
  const chinese = require(path.join(temporary, 'i18n/locales/zh-CN.js')).zhCN;
  for (const key of [
    'users_daily_tab',
    'col_date',
    'col_user',
    'col_keys',
    'filter_user',
    'unknown_user',
    'unnamed_key',
    'users_note',
    'invalid_user_range',
    'ranking_title',
    'ranking_note',
    'ranking_share',
    'ranking_empty',
    'ranking_unavailable',
    'ranking_dashboard_title',
    'ranking_users_tab',
    'ranking_keys_tab',
    'ranking_keys_title',
    'ranking_models_tab',
    'ranking_models_title',
    'unknown_model',
  ]) {
    assert.equal(typeof english.usage[key], 'string', key);
    assert.equal(typeof chinese.usage[key], 'string', key);
  }
  assert.match(english.usage.users_note, /UTC/);
  assert.match(english.usage.users_note, /all pages/);
  assert.match(english.usage.users_note, /organization/);
});

const { formatUsageCount, tokenPercentage } = require(
  path.join(temporary, 'features/usage/tokenRanking.js'),
);

test('usage counts use readable Chinese units with at most two decimal places', () => {
  for (const [tokens, display] of [
    [0, '0'],
    [707, '707'],
    [9_999, '9,999'],
    [10_000, '1 万'],
    [12_345, '1.23 万'],
    [49_523, '4.95 万'],
    [999_999, '100 万'],
    [1_000_000, '1 百万'],
    [9_053_070, '9.05 百万'],
    [97_249_570, '97.25 百万'],
    [99_999_999, '100 百万'],
    [100_000_000, '1 亿'],
    [264_906_784, '2.65 亿'],
    [1_210_000_000, '12.1 亿'],
    [1_240_759_545, '12.41 亿'],
    [1_249_812_615, '12.5 亿'],
  ]) {
    assert.equal(formatUsageCount(tokens), display, String(tokens));
  }
});

test('usage units follow the interface language and handle unavailable values', () => {
  for (const [tokens, display] of [
    [999, '999'],
    [1_000, '1 K'],
    [1_234_567, '1.23 M'],
    [1_210_000_000, '1.21 B'],
  ]) {
    assert.equal(formatUsageCount(tokens, 'en-US'), display, String(tokens));
  }
  for (const tokens of [NaN, Infinity, -Infinity]) {
    assert.equal(formatUsageCount(tokens), '0');
  }
});

test('ranking shares use the full filtered total', () => {
  assert.equal(tokenPercentage(50, 100), 50);
  assert.equal(tokenPercentage(100, 100), 100);
  assert.equal(tokenPercentage(50, 1000), 5);
  assert.equal(tokenPercentage(1, 1000), 0.1);
  assert.equal(tokenPercentage(200, 100), 100);
});

test('empty or unavailable token totals produce zero shares', () => {
  for (const [tokens, total] of [
    [0, 0],
    [10, 0],
    [-1, 100],
    [NaN, 100],
    [100, Infinity],
    [Infinity, 100],
    [100, -1],
  ]) {
    assert.equal(tokenPercentage(tokens, total), 0);
  }
});

const { keyRankingItems, modelRankingItems, organizationRankingItems } = require(
  path.join(temporary, 'features/usage/tokenRanking.js'),
);
const keyRankingLabels = {
  unnamedKey: 'Unnamed Key',
  unknownOrganization: 'Unknown organization',
};

test('keys remain distinct despite duplicate names or a shared organization', () => {
  const rows = keyRankingItems(
    [
      {
        api_key_id: 42,
        api_key_name: 'Development',
        organization_name: 'Engineering',
        input_tokens: 80,
        output_tokens: 20,
        total_tokens: 100,
      },
      {
        api_key_id: 43,
        api_key_name: 'Development',
        organization_name: 'Engineering',
        input_tokens: 40,
        output_tokens: 10,
        total_tokens: 50,
      },
      { api_key_id: 44, api_key_name: '', input_tokens: 10, output_tokens: 5, total_tokens: 15 },
    ],
    keyRankingLabels,
  );
  assert.deepEqual(
    rows.map(row => row.id),
    [42, 43, 44],
  );
  assert.deepEqual(
    rows.map(row => row.label),
    ['Development (Engin)', 'Development (Engin)', 'Unnamed Key (Unknown organization)'],
  );
  assert.deepEqual(
    rows.map(row => row.total_tokens),
    [100, 50, 15],
  );
});

test('all ranking tabs preserve empty and unavailable states and safe identity fallbacks', () => {
  assert.equal(keyRankingItems(undefined, keyRankingLabels), undefined);
  assert.equal(modelRankingItems(undefined, 'Unknown model'), undefined);
  assert.deepEqual(modelRankingItems([], 'Unknown model'), []);
  assert.equal(organizationRankingItems(undefined, 'Unattributed'), undefined);
  assert.deepEqual(keyRankingItems([], keyRankingLabels), []);
  const users = organizationRankingItems(
    [
      {
        organization_id: 7,
        organization_name: '',
        input_tokens: 10,
        output_tokens: 5,
        total_tokens: 15,
      },
      {
        organization_id: 0,
        organization_name: '',
        input_tokens: 10,
        output_tokens: 5,
        total_tokens: 15,
      },
    ],
    'Unattributed',
  );
  assert.deepEqual(
    users.map(row => row.label),
    ['#7', 'Unattributed'],
  );
});

test('key labels use five organization characters and retain full names in tooltips', () => {
  const rows = keyRankingItems(
    [
      {
        api_key_id: 1,
        api_key_name: 'knox-glm',
        organization_name: 'TypeX Technology',
        input_tokens: 80,
        output_tokens: 20,
        total_tokens: 100,
      },
      {
        api_key_id: 2,
        api_key_name: '开发 Key',
        organization_name: '研发中心平台组织',
        input_tokens: 40,
        output_tokens: 10,
        total_tokens: 50,
      },
      {
        api_key_id: 3,
        api_key_name: 'Key',
        organization_name: '😀Alpha Team',
        input_tokens: 20,
        output_tokens: 5,
        total_tokens: 25,
      },
      {
        api_key_id: 4,
        api_key_name: 'Key',
        organization_name: 'BOS',
        input_tokens: 10,
        output_tokens: 5,
        total_tokens: 15,
      },
      {
        api_key_id: 5,
        api_key_name: 'Transferred',
        organization_name: 'Current organization',
        organization_count: 2,
        input_tokens: 10,
        output_tokens: 5,
        total_tokens: 15,
      },
    ],
    keyRankingLabels,
  );
  assert.deepEqual(
    rows.map(row => row.label),
    [
      'knox-glm (TypeX)',
      '开发 Key (研发中心平)',
      'Key (😀Alph)',
      'Key (BOS)',
      'Transferred (Curre)',
    ],
  );
  assert.deepEqual(
    rows.map(row => row.tooltipLabel),
    [
      'knox-glm (TypeX Technology)',
      '开发 Key (研发中心平台组织)',
      'Key (😀Alpha Team)',
      'Key (BOS)',
      'Transferred (Current organization)',
    ],
  );
});

test('model rankings retain model identities, full usage and the server ranking order', () => {
  const models = [
    { model: 'popular-model', input_tokens: 90, output_tokens: 10, total_tokens: 100 },
    { model: '', input_tokens: 40, output_tokens: 10, total_tokens: 50 },
    { model: 'Unknown model', input_tokens: 20, output_tokens: 5, total_tokens: 25 },
  ];
  const rows = modelRankingItems(models, 'Unknown model');
  assert.deepEqual(rows, [
    { ...models[0], id: 'popular-model', label: 'popular-model' },
    { ...models[1], id: '', label: 'Unknown model' },
    { ...models[2], id: 'Unknown model', label: 'Unknown model' },
  ]);
  assert.equal(tokenPercentage(rows[0].total_tokens, 1000), 10);
});
