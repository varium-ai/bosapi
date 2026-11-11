const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const react = require('react');
const root = path.resolve(__dirname, '../src');
function load(file, mocks = {}) {
  const exports = {};
  const code = ts.transpileModule(fs.readFileSync(path.join(root, file), 'utf8'), {
    fileName: file,
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      jsx: ts.JsxEmit.ReactJSX,
    },
  }).outputText;
  vm.runInNewContext(code, {
    exports,
    URLSearchParams,
    window: mocks.window,
    require: name =>
      Object.hasOwn(mocks, name)
        ? mocks[name]
        : name === 'dayjs/plugin/utc' || name === 'dayjs/plugin/timezone'
          ? { default: require(name) }
          : name.endsWith('.css')
            ? {}
            : require(name),
  });
  return exports;
}
const pricing = load('features/providers/pricingState.ts');
test('pricing distinguishes legacy zero, explicit free rates and positive model prices', () => {
  const zero = { model_name: 'model', price_input_per_1m: 0, price_output_per_1m: 0 };
  for (const price of [undefined, {}, { input: 0, output: 0 }]) {
    assert.equal(pricing.defaultPriceConfirmed(price), false);
    assert.equal(pricing.modelPriceSource(price, zero), 'unknown');
  }
  assert.equal(
    pricing.modelPriceSource({ models: { model: { input: 0, output: 0 } } }, zero),
    'model',
  );
  assert.equal(pricing.modelPriceSource({ input: 0, output: 0, confirmed: true }, zero), 'default');
  assert.equal(pricing.modelPriceSource({}, { ...zero, price_input_per_1m: 0.2 }), 'recorded');
  assert.equal(
    pricing.modelPriceSource(
      { input: 1, output: 2 },
      { ...zero, price_input_per_1m: 1, price_output_per_1m: 2 },
    ),
    'default',
  );
  assert.equal(pricing.modelPriceSource({ input: 1, output: 2 }, zero), 'unknown');
  assert.equal(pricing.defaultPriceConfirmed({ input: Infinity, output: NaN }), false);
});
test('saving without prices preserves uncertainty and overrides; entering zero confirms the rate', () => {
  const existing = { models: { model: { input: 1, output: 2 } } };
  assert.deepEqual(JSON.parse(JSON.stringify(pricing.saveDefaultPricing(existing))), existing);
  const free = pricing.saveDefaultPricing(existing, 0, 0);
  assert.equal(free.confirmed, true);
  assert.equal(free.input, 0);
  assert.equal(free.output, 0);
  assert.equal(free.models, existing.models);
});

test('prompt-cache pricing is preserved, explicitly free, and submitted with rates', () => {
  const existing = {
    input: 1,
    output: 2,
    cache_read: 0.2,
    cache_write: 2,
    models: { model: { input: 1, output: 2, cache_read: 0.1, cache_write: 1.5 } },
  };
  assert.deepEqual(JSON.parse(JSON.stringify(pricing.saveDefaultPricing(existing))), existing);
  const free = pricing.saveDefaultPricing(existing, 0, 0, 0, 0);
  assert.equal(free.confirmed, true);
  assert.equal(free.cache_read, 0);
  assert.equal(free.cache_write, 0);
  assert.equal(free.models.model.cache_read, 0.1);
  const changed = pricing.saveDefaultPricing(existing, 2, 4, 0.4, 3);
  assert.deepEqual(
    {
      input: changed.input,
      output: changed.output,
      cache_read: changed.cache_read,
      cache_write: changed.cache_write,
    },
    { input: 2, output: 4, cache_read: 0.4, cache_write: 3 },
  );
});

function pageHarness(file, { search = '', logs = {}, draft = {} } = {}) {
  const queries = [];
  const mutations = [];
  const effects = [];
  const changes = [];
  const params = new URLSearchParams(search);
  const form = { submit() {}, isFieldsTouched: () => false };
  const hooks = {
    ...react,
    useState: initial => [typeof initial === 'function' ? initial() : initial, () => {}],
    useRef: initial => ({ current: initial }),
    useEffect: () => {},
  };
  function Form() {}
  Form.Item = 'Form.Item';
  Form.useForm = () => [form];
  Form.useWatch = () => draft;
  const common = new Proxy({}, { get: (_, key) => key });
  const mocks = {
    react: { ...hooks, default: hooks },
    dayjs: { default: require('dayjs') },
    antd: new Proxy(
      { Form, Typography: { Text: 'Text', Paragraph: 'Paragraph', Link: 'Link' }, message: {} },
      { get: (target, key) => target[key] || key },
    ),
    '@ant-design/icons': common,
    '@tanstack/react-query': {
      useQuery: options => {
        queries.push(options);
        if (options.queryKey[0] === 'admin-request-logs') return { data: { items: [] }, ...logs };
        return { data: options.queryKey[0] === 'orgs' ? [] : undefined };
      },
      useMutation: options => {
        mutations.push(options);
        return { ...options, isPending: false };
      },
      useQueryClient: () => ({ invalidateQueries: () => effects.push('refresh') }),
      useIsFetching: () => 0,
    },
    'react-router-dom': {
      useSearchParams: () => [params, next => changes.push(next)],
      useOutletContext: () => ({ user: { role: 'super_admin' } }),
      useLocation: () => ({ pathname: '/admin/request-logs', search }),
      useNavigate: () => () => {},
      Link: 'Link',
    },
    '../../api/client': { apiClient: {} },
    '../../api/adminQueries': { organizationOptionsQueryOptions: () => ({ queryKey: ['orgs'] }) },
    '../../i18n': {
      useI18n: () => ({
        t: key => key,
        formatDateTime: String,
        formatNumber: String,
        formatTokens: String,
        formatCurrency: String,
        timezoneBadge: 'UTC+8',
        timezone: 'UTC',
      }),
    },
    '../../i18n/formatters': { resolveTimezone: timezone => timezone },
    '../common/ConsoleUX': new Proxy(
      { useDiscardChanges: () => () => {} },
      { get: (target, key) => target[key] || key },
    ),
    '../common/useDebouncedValue': { useDebouncedValue: value => value },
    '../common/ux': { validRequestID: () => true, stepKey: value => value, routeTarget: () => '' },
    './userUsageFilters': load('features/usage/userUsageFilters.ts'),
    './UsageSnapshotStatus': load('features/usage/UsageSnapshotStatus.tsx', {
      '../../i18n': {},
    }),
    './usageQueryCache': {
      usageRefreshParams: () => ({}),
      refreshUsageQueries: () => effects.push('refresh'),
    },
    './requestTiming': { formatFirstTokenLatency: String },
    './diagnostics': { logFields: () => ({}), entryDiagnosis: () => ({}) },
  };
  const source = fs.readFileSync(path.join(root, file), 'utf8');
  for (const match of source.matchAll(/from ['"]([^'"]+)['"]/g)) {
    if (match[1].startsWith('.') && !Object.hasOwn(mocks, match[1])) mocks[match[1]] = common;
  }
  return { module: load(file, mocks), queries, mutations, changes, effects };
}
function elements(node, predicate) {
  if (!node || typeof node !== 'object') return [];
  if (Array.isArray(node)) return node.flatMap(child => elements(child, predicate));
  const found = node.type && predicate(node) ? [node] : [];
  return found.concat(...Object.values(node.props || {}).map(child => elements(child, predicate)));
}

test('all usage views reject inverted dates, including manual refresh, and clear preserves the view', () => {
  for (const view of ['records', 'daily', 'weekly', 'monthly', 'organizations']) {
    const ui = pageHarness('features/usage/UsagePage.tsx', {
      search: `view=${view}&from=2026-10-30&to=2026-10-01`,
    });
    const tree = ui.module.UsagePage({});
    for (const query of ui.queries.filter(item =>
      ['admin-usage-records', 'admin-usage-agg'].includes(item.queryKey[0]),
    ))
      assert.equal(query.enabled, false);
    const refresh = elements(tree, node => node.type === 'RefreshButton')[0];
    refresh.props.onClick();
    assert.equal(ui.effects.length, 0);
    const clear = elements(
      tree,
      node => node.type === 'Button' && node.props.children === 'ux.clear',
    )[0];
    clear.props.onClick();
    assert.equal(ui.changes.at(-1).view, view);
  }
  const corrected = pageHarness('features/usage/UsagePage.tsx', {
    search: 'view=records&from=2026-10-01&to=2026-10-30',
  });
  corrected.module.UsagePage({});
  assert.equal(
    corrected.queries.find(item => item.queryKey[0] === 'admin-usage-records').enabled,
    true,
  );
});

test('usage views expose prompt-cache tokens, costs, and hit rate', () => {
  const usageSource = fs.readFileSync(path.join(root, 'features/usage/UsagePage.tsx'), 'utf8');
  const dashboardSource = fs.readFileSync(
    path.join(root, 'features/usage/DailyOrganizationUsage.tsx'),
    'utf8',
  );
  for (const source of [usageSource, dashboardSource]) {
    for (const field of [
      'cache_read_tokens',
      'cache_write_tokens',
      'cache_hit_rate',
      'cache_read_cost_amount',
      'cache_write_cost_amount',
    ]) {
      assert.match(source, new RegExp(field));
    }
  }
});

test('request trace distinguishes empty, loading and recorded results', () => {
  for (const [logs, expected] of [
    [{ data: { items: [] }, isLoading: false }, false],
    [{ data: undefined, isLoading: true }, false],
    [{ data: undefined, isError: true }, false],
    [{ data: { items: [{ id: 1, step: 'completed', status: 'ok' }] }, isLoading: false }, true],
  ]) {
    const ui = pageHarness('features/requestlogs/RequestLogsPage.tsx', {
      search: '?request_id=valid-id',
      logs,
    });
    const tree = ui.module.RequestLogsPage();
    assert.equal(elements(tree, node => node.type === 'TraceDiagnosis').length > 0, expected);
  }
});

// Exercise the feedback component's event lifecycle with deterministic hook scheduling.
test('configuration feedback stays local for field errors and clears on close, cancel and navigation', () => {
  const state = [],
    effects = [],
    listeners = new Map();
  let cursor = 0,
    pathname = '/admin/providers';
  const hook = {
    useState: initial => {
      const index = cursor++;
      if (!(index in state)) state[index] = initial;
      return [
        state[index],
        value => {
          state[index] = value;
        },
      ];
    },
    useEffect: (effect, deps) => {
      const index = cursor++;
      const prev = effects[index];
      if (!prev || prev.deps.some((value, i) => value !== deps[i])) {
        prev?.cleanup?.();
        effects[index] = { deps, pending: effect };
      }
    },
  };
  const window = {
    addEventListener: (type, fn) => listeners.set(type, fn),
    removeEventListener: type => listeners.delete(type),
  };
  const ui = load('features/diagnostics/ConfigurationFeedback.tsx', {
    react: hook,
    window,
    antd: { Alert: 'Alert', Button: 'Button', Space: 'Space', Typography: { Text: 'Text' } },
    '../../i18n': { useI18n: () => ({ language: 'en-US' }) },
    'react-router-dom': { useLocation: () => ({ pathname }) },
    './catalog': load('features/diagnostics/catalog.ts'),
    '../common/copyText': { copyText: async () => true },
  });
  const render = () => {
    cursor = 0;
    const tree = ui.ConfigurationFeedback();
    for (const effect of effects)
      if (effect?.pending) {
        effect.cleanup = effect.pending();
        effect.pending = undefined;
      }
    return tree;
  };
  const emit = code =>
    listeners.get('bos:configuration-error')({
      detail: { response: { data: { error: { code } } } },
    });
  render();
  listeners.get('bos:configuration-error')({
    detail: {
      config: { _configurationPath: '/admin/keys' },
      response: { data: { error: { code: 'provider_auth_failed' } } },
    },
  });
  assert.equal(render(), null);
  emit('field_validation');
  assert.equal(render(), null);
  emit('provider_auth_failed');
  let tree = render();
  assert.ok(tree);
  elements(tree, node => node.type === 'Alert')[0].props.onClose();
  assert.equal(render(), null);
  emit('provider_auth_failed');
  assert.ok(render());
  listeners.get('bos:configuration-clear')();
  assert.equal(render(), null);
  emit('provider_auth_failed');
  pathname = '/admin/usage';
  render();
  assert.equal(render(), null);
});

test('Key readiness warns for unavailable organization bindings even when a channel catalog has models', () => {
  const run = ({ providerID = 1, error = false, selectedModels = [] } = {}) => {
    const module = load('features/keys/KeyAccessFields.tsx', {
      antd: {
        Alert: 'Alert',
        Button: 'Button',
        Spin: 'Spin',
        Select: 'Select',
        Typography: { Link: 'Link' },
        Form: {
          useFormInstance: () => ({}),
          useWatch: name => (name === 'allowed_models' ? selectedModels : []),
        },
      },
      '@tanstack/react-query': {
        useQuery: options =>
          options.queryKey[0] === 'key-channel-options'
            ? { isSuccess: true, data: [{ id: 1, name: 'Shared catalog' }] }
            : { isSuccess: !error, isError: error, data: { providerID } },
        useQueries: () => [{ isSuccess: true, data: [{ model_name: 'model', status: 'active' }] }],
      },
      '../../api/client': { apiClient: {} },
      '../../i18n': { useI18n: () => ({ t: key => key }) },
      '../onboarding/firstKey': load('features/onboarding/firstKey.ts'),
      '../common/ConsoleUX': { LoadError: 'LoadError' },
      './keyModelOptions': load('features/keys/keyModelOptions.ts'),
      './key-access.css': {},
    });
    return module.KeyAccessReadiness({
      enabled: true,
      organizationID: 7,
      canManageOrganization: true,
    });
  };
  assert.equal(run(), null);
  assert.equal(run({ providerID: 0 }).props.message, 'qa.no_access');
  assert.equal(run({ selectedModels: ['unavailable-model'] }).props.message, 'qa.no_access');
  assert.equal(run({ error: true }).type, 'LoadError');
  const link = elements(run({ providerID: 0 }), node => node.type === 'Link')[0];
  assert.match(link.props.href, /setup_org=7/);
  assert.match(link.props.href, /setup_action=edit-organization/);
});
