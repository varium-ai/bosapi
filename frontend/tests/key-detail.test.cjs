const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const root = path.resolve(__dirname, '../src');

function loader(mocks) {
  const cache = new Map();
  const load = file => {
    file = path.resolve(file);
    if (cache.has(file)) return cache.get(file);
    const exports = {};
    cache.set(file, exports);
    const source = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      fileName: file,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2020,
        jsx: ts.JsxEmit.ReactJSX,
      },
    }).outputText;
    vm.runInNewContext(source, {
      exports,
      require: name => {
        if (name.endsWith('.css')) return {};
        if (Object.hasOwn(mocks, name)) return mocks[name];
        if (!name.startsWith('.')) return require(name);
        const target = path.resolve(path.dirname(file), name);
        return load(fs.existsSync(target + '.ts') ? target + '.ts' : target + '.tsx');
      },
    });
    return exports;
  };
  return file => load(path.join(root, file));
}

function elements(node, predicate) {
  if (!node || typeof node !== 'object') return [];
  if (Array.isArray(node)) return node.flatMap(child => elements(child, predicate));
  return (node.type && predicate(node) ? [node] : []).concat(
    ...Object.values(node.props || {}).map(child => elements(child, predicate)),
  );
}

const i18n = {
  useI18n: () => ({
    t: key => key,
    formatTokens: String,
    formatNumber: String,
    formatCurrency: String,
  }),
};

test('Key detail usage cursors survive next/previous and reset for Key, IP, and day changes', async () => {
  const states = [];
  let slot = 0;
  let options;
  let response = { items: [], total: null, has_more: true, next_cursor: 'next-2' };
  const calls = [];
  const load = loader({
    react: {
      useEffect() {},
      useState: initial => {
        const index = slot++;
        if (!(index in states)) states[index] = typeof initial === 'function' ? initial() : initial;
        return [
          states[index],
          next => {
            states[index] = typeof next === 'function' ? next(states[index]) : next;
          },
        ];
      },
    },
    '@tanstack/react-query': {
      useQuery: query => {
        options = query;
        return {};
      },
    },
    '../../api/client': {
      apiClient: {
        get: async (url, config) => {
          calls.push({ url, ...config });
          return { data: { data: response } };
        },
      },
    },
  });
  const { useAdminKeyUsageRecords } = load('features/keys/useAdminKeyUsageRecords.ts');
  const render = (key = 149, ip, enabled = true) => {
    slot = 0;
    return useAdminKeyUsageRecords(key, enabled, ip);
  };
  render();
  states[0] = new Date('2026-11-02T01:00:00Z');
  let result = render();
  const signal = new AbortController().signal;
  const page = await options.queryFn({ signal });
  assert.equal(page.total, null);
  assert.equal(calls.at(-1).url, '/admin/usage');
  assert.equal(calls.at(-1).params.page, undefined);
  assert.equal(calls.at(-1).params.include_total, undefined);
  assert.equal(calls.at(-1).params.from, '2026-11-02');
  assert.equal(calls.at(-1).params.to, '2026-11-02');
  assert.equal(calls.at(-1).signal, signal);
  result.pagination.next(page.next_cursor);
  result = render();
  assert.equal(result.pagination.page, 2);
  await options.queryFn({ signal });
  assert.equal(calls.at(-1).params.cursor, 'next-2');
  result.pagination.previous();
  result = render();
  assert.equal(result.pagination.page, 1);
  for (const change of [
    () => render(150),
    () => render(150, '127.0.0.1'),
    () => {
      states[0] = new Date('2026-11-02T16:01:00Z');
      return render(150, '127.0.0.1');
    },
  ]) {
    result.pagination.next('another-page');
    result = change();
    assert.equal(result.pagination.page, 1);
    assert.equal(result.pagination.cursor, '');
  }
  await options.queryFn({ signal });
  assert.equal(calls.at(-1).params.from, '2026-11-03');
  assert.equal(calls.at(-1).params.client_ip, '127.0.0.1');
  render(150, undefined, false);
  assert.equal(options.enabled, false);
  response = { items: [], total: 0 };
  await assert.rejects(options.queryFn({ signal }), /Invalid usage cursor response/);
});

test('unavailable display statistics do not mask live lifetime usage or become measured zero', () => {
  const load = loader({
    antd: { Table: 'Table', Typography: { Text: 'Text' } },
    '../../i18n': i18n,
  });
  const { KeyQuotaTable } = load('features/keys/KeyQuotaTable.tsx');
  for (const status of ['generating', 'unavailable', 'expired']) {
    const tree = KeyQuotaTable({
      data: {
        usage_stats_status: status,
        daily_used_tokens: 0,
        quota_used_tokens: 25,
        quota_used_amount: 2,
      },
    });
    const rows = tree.props.dataSource;
    assert.equal(rows[0].tokenUsed, '-');
    assert.equal(rows[0].amountUsed, '-');
    assert.equal(rows[3].tokenUsed, '25');
    assert.equal(rows[3].amountUsed, '2');
  }
  const ready = KeyQuotaTable({ data: { usage_stats_status: 'ready', daily_used_tokens: 0 } });
  assert.equal(ready.props.dataSource[0].tokenUsed, '0');
});

test('pending aggregate snapshot hides totals while keeping period selection available', () => {
  const load = loader({
    antd: { Card: 'Card', Statistic: 'Statistic', Table: 'Table' },
    '../../i18n': i18n,
    '../common/ConsoleUX': { LoadError: 'LoadError' },
    '../common/SegmentedControl': { SegmentedControl: 'SegmentedControl' },
  });
  const { KeyUsageSummary } = load('features/keys/KeyUsageSummary.tsx');
  const tree = KeyUsageSummary({
    period: 'total',
    points: [],
    available: false,
    loading: false,
    failed: false,
    onPeriodChange() {},
    retry() {},
  });
  assert.equal(elements(tree, node => node.type === 'Statistic').length, 0);
  assert.equal(elements(tree, node => node.type === 'SegmentedControl').length, 1);
});

test('IP snapshot pages share a version, reset on replacement and Key changes, and cancel inactive work', async () => {
  const states = [];
  let slot = 0;
  let options;
  let effects = [];
  let data;
  const calls = [];
  const valid = new Date(Date.now() + 60000).toISOString();
  const load = loader({
    react: {
      useEffect: fn => effects.push(fn),
      useState: initial => {
        const index = slot++;
        if (!(index in states)) states[index] = typeof initial === 'function' ? initial() : initial;
        return [
          states[index],
          next => {
            states[index] = typeof next === 'function' ? next(states[index]) : next;
          },
        ];
      },
    },
    antd: { Alert: 'Alert' },
    '../../i18n': i18n,
    '@tanstack/react-query': {
      useQuery: query => {
        options = query;
        return { data };
      },
    },
    '../../api/client': {
      apiClient: {
        get: async (url, config) => {
          calls.push({ url, ...config });
          return { data: { data } };
        },
      },
    },
  });
  const { useKeyIPUsage } = load('features/keys/useKeyIPUsage.ts');
  const render = (key = 149, enabled = true, portal = false) => {
    slot = 0;
    effects = [];
    const result = useKeyIPUsage(key, enabled, portal);
    effects.forEach(fn => fn());
    return result;
  };
  data = { items: [], total: 0, snapshot: { status: 'generating' } };
  let result = render();
  assert.equal(result.windowMinutes, 30);
  assert.equal(result.usable, false);
  assert.equal(options.retry, false);
  assert.equal(options.refetchInterval({ state: { data } }), 2000);
  data = {
    items: [{ client_ip: '192.0.2.1' }],
    total: 2,
    snapshot: { status: 'ready', snapshot_id: 'v1', valid_until: valid },
  };
  render();
  result = render();
  result.setPage(2);
  result = render();
  assert.equal(result.page, 2);
  assert.equal(result.usable, true);
  const signal = new AbortController().signal;
  await options.queryFn({ signal });
  assert.equal(calls.at(-1).url, '/admin/usage/ips');
  assert.equal(calls.at(-1).params.window_minutes, 30);
  assert.equal(calls.at(-1).params.snapshot_id, 'v1');
  assert.equal(calls.at(-1).params.page, 2);
  assert.equal(calls.at(-1).signal, signal);
  assert.equal(options.refetchInterval({ state: { data } }), 30000);
  data = {
    items: [],
    total: 0,
    snapshot: { status: 'replaced', snapshot_id: 'v2', valid_until: valid },
  };
  result = render();
  assert.equal(result.usable, false);
  result = render();
  assert.equal(result.page, 1);
  await options.queryFn({ signal });
  assert.equal(calls.at(-1).params.snapshot_id, undefined);
  data = undefined;
  result.setPage(2);
  result = render(150, false);
  assert.equal(result.page, 1);
  assert.equal(options.enabled, false);
  assert.equal(options.queryKey[1], 150);
  for (const status of ['expired', 'unavailable']) {
    data = { items: [], total: 0, snapshot: { status } };
    assert.equal(render(150).usable, false);
  }
  data = { items: [], total: 0, snapshot: { status: 'generating' } };
  result = render(150);
  for (const minutes of [60, 360, 720, 1440, 30]) {
    result.setPage(2);
    result = render(150);
    result.setWindowMinutes(minutes);
    result = render(150);
    assert.equal(result.page, 1);
    assert.equal(result.windowMinutes, minutes);
    assert.equal(options.queryKey[3], minutes);
    await options.queryFn({ signal });
    assert.equal(calls.at(-1).params.window_minutes, minutes);
    assert.equal(calls.at(-1).params.snapshot_id, undefined);
  }
  result = render(150, true, true);
  assert.equal(result.page, 1);
  await options.queryFn({ signal });
  assert.equal(calls.at(-1).url, '/key-portal/ips');
  assert.equal(calls.at(-1).params.key_id, undefined);
  data = { items: null, total: 0 };
  await assert.rejects(options.queryFn({ signal }), /Invalid IP usage response/);
});

test('IP range selector offers exactly five presets capped at 24 hours', () => {
  const load = loader({
    antd: { Space: 'Space', Typography: { Text: 'Text' } },
    '../../i18n': i18n,
    '../common/SegmentedControl': { SegmentedControl: 'SegmentedControl' },
  });
  const { KeyIPTimeRange } = load('features/keys/KeyIPTimeRange.tsx');
  let selected;
  const tree = KeyIPTimeRange({
    value: 1440,
    onChange: value => {
      selected = value;
    },
  });
  const selector = elements(tree, node => node.type === 'SegmentedControl')[0];
  assert.deepEqual(
    Array.from(selector.props.options, option => option.value),
    [30, 60, 360, 720, 1440],
  );
  assert.equal(selector.props.value, 1440);
  selector.props.onChange(30);
  assert.equal(selected, 30);
});
