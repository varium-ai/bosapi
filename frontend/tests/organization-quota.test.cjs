const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const { QueryClient } = require('@tanstack/react-query');

// Unit harness matching the VM/transpile convention used by frontend component tests.
// Effects, queries and transport are controlled; no DOM, server or browser is used.
const root = path.resolve(__dirname, '../src');
const admin = { id: 1, role: 'super_admin', organization_id: 10 };
const tenant = { id: 2, role: 'org_admin', organization_id: 10 };
const plain = value => JSON.parse(JSON.stringify(value));
const jsx = (type, props, key) => ({ type, props: props || {}, key });
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function nodes(tree, predicate) {
  if (Array.isArray(tree)) return tree.flatMap(item => nodes(item, predicate));
  if (!tree || typeof tree !== 'object') return [];
  return [...(predicate(tree) ? [tree] : []), ...nodes(tree.props?.children, predicate)];
}
function textContent(tree) {
  if (Array.isArray(tree)) return tree.map(textContent).join(' ');
  if (tree == null || tree === false) return '';
  return typeof tree === 'object' ? textContent(tree.props?.children) : String(tree);
}
function snapshot(id = 10, quotas = {}) {
  return {
    degraded: false,
    runtime_state: 'redis_counter',
    global: { qps: 15 },
    organization: {
      organization_id: id,
      qps: 20,
      quotas,
      usage: { daily_tokens: 42, daily_amount: 0.00000001, weekly_tokens: 80, monthly_tokens: 90 },
    },
  };
}
function result(data, changes = {}) {
  return {
    data,
    dataUpdatedAt: 1,
    isSuccess: !!data,
    isPending: !data,
    isLoading: !data,
    isFetching: false,
    isError: false,
    refetch: async () => {},
    ...changes,
  };
}
function harness(user = admin, search = 'organization_id=10') {
  let active;
  const state = {
    user,
    params: new URLSearchParams(search),
    options: [],
    responses: new Map(),
    gets: [],
    puts: [],
    invalidations: [],
    errors: [],
    successes: [],
  };
  const hooks = {
    useState(initial) {
      const mount = active,
        index = mount.cursor++;
      if (!(index in mount.slots))
        mount.slots[index] = typeof initial === 'function' ? initial() : initial;
      return [
        mount.slots[index],
        value => {
          const next = typeof value === 'function' ? value(mount.slots[index]) : value;
          if (!Object.is(next, mount.slots[index])) {
            mount.slots[index] = next;
            mount.dirty = true;
          }
        },
      ];
    },
    useRef(initial) {
      return hooks.useState(() => ({ current: initial }))[0];
    },
    useEffect(effect, deps) {
      const mount = active,
        index = mount.cursor++;
      const previous = mount.slots[index];
      if (!previous || deps.some((value, i) => !Object.is(value, previous.deps[i]))) {
        mount.effects.push(() => {
          previous?.cleanup?.();
          mount.slots[index] = { deps, cleanup: effect() };
        });
      }
    },
  };
  function Form() {}
  Form.Item = 'Form.Item';
  Form.useForm = () => [
    hooks.useState(() => ({
      values: {},
      setFieldsValue(values) {
        Object.assign(this.values, values);
      },
    }))[0],
  ];
  const client = {
    get: async (...args) => {
      state.gets.push(args);
      return state.get?.(...args);
    },
    put: async (...args) => {
      state.puts.push(args);
      return state.put?.(...args);
    },
  };
  const cache = new Map();
  const mocks = {
    react: hooks,
    'react/jsx-runtime': { jsx, jsxs: jsx, Fragment: 'Fragment' },
    antd: {
      ...Object.fromEntries(
        [
          'Card',
          'InputNumber',
          'Button',
          'Space',
          'Row',
          'Col',
          'Tag',
          'Alert',
          'Select',
          'Divider',
          'Badge',
          'Table',
          'Progress',
          'Skeleton',
        ].map(name => [name, name]),
      ),
      Form,
      Typography: { Text: 'Text', Paragraph: 'Paragraph' },
      Descriptions: Object.assign(function Descriptions() {}, { Item: 'Descriptions.Item' }),
      message: {
        error: value => state.errors.push(value),
        success: value => state.successes.push(value),
      },
    },
    '@ant-design/icons': {},
    '@tanstack/react-query': {
      queryOptions: value => value,
      useQuery: options => {
        state.options.push(options);
        return state.responses.get(JSON.stringify(options.queryKey)) || result(undefined);
      },
      useQueryClient: () => ({
        invalidateQueries: async options => {
          state.invalidations.push(options);
          return state.invalidate?.(options);
        },
      }),
    },
    'react-router-dom': {
      useOutletContext: () => ({ user: state.user }),
      useSearchParams: () => [
        state.params,
        params => {
          state.params = params;
        },
      ],
    },
  };
  function load(relative) {
    const filename = path.join(root, relative);
    if (cache.has(filename)) return cache.get(filename);
    const exports = {};
    cache.set(filename, exports);
    const source = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2020,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    }).outputText;
    vm.runInNewContext(
      source,
      {
        exports,
        URLSearchParams,
        AbortController,
        require: name => {
          if (mocks[name]) return mocks[name];
          if (name.endsWith('/api/client'))
            return { default: client, apiClient: client, __esModule: true };
          if (name.endsWith('/i18n'))
            return {
              useI18n: () => ({
                t: key => key,
                language: 'en-US',
                formatTokens: value => String(value),
                formatCurrency: value => `$${value}`,
              }),
            };
          if (name.endsWith('/i18n/formatters'))
            return { formatDateTime: (value, zone) => `${value} ${zone}` };
          if (name.endsWith('/common/display')) return { compactKey: () => 'key' };
          if (name.endsWith('/common/RefreshButton'))
            return { RefreshButton: function RefreshButton() {} };
          if (name.endsWith('/common/ConsoleUX'))
            return { LoadError: 'LoadError', PageHelp: 'PageHelp' };
          if (name.endsWith('/onboarding/setupQueries'))
            return {
              loadSetupPages: async (...args) => {
                state.pages = args;
                return [];
              },
            };
          const resolved = path.resolve(path.dirname(filename), name);
          const extension = fs.existsSync(`${resolved}.ts`) ? '.ts' : '.tsx';
          return load(path.relative(root, resolved + extension));
        },
      },
      { filename },
    );
    return exports;
  }
  function mount(type, props) {
    const instance = {
      slots: [],
      effects: [],
      cursor: 0,
      dirty: false,
      render() {
        let attempts = 0;
        do {
          assert.ok(attempts++ < 10, 'render should stabilize');
          this.dirty = false;
          this.cursor = 0;
          this.effects = [];
          active = this;
          this.tree = type(props);
          this.effects.forEach(effect => effect());
        } while (this.dirty);
        return this.tree;
      },
      unmount() {
        this.slots.forEach(slot => slot?.cleanup?.());
      },
    };
    instance.render();
    return instance;
  }
  const model = load('features/ratelimits/organizationQuota.ts');
  function respond(options, data, changes) {
    state.responses.set(JSON.stringify(options.queryKey), result(data, changes));
  }
  function page(data = snapshot(), changes = {}) {
    respond({ queryKey: ['rate-limit-organizations', ...model.quotaScope(state.user)] }, [
      { id: 10, name: 'Tenant A' },
      { id: 20, name: 'Tenant B' },
    ]);
    const outer = load('features/ratelimits/RateLimitsPage.tsx').RateLimitsPage();
    if (!outer) return { outer };
    const scoped = mount(outer.type, outer.props);
    const selection = scoped.tree;
    respond(model.rateLimitQueryOptions(state.user, selection.props.selectedOrgId), data, changes);
    const editor = mount(selection.type, selection.props);
    return { outer, scoped, editor, selection };
  }
  return { ...state, state, load, model, mount, respond, page, Form };
}

const form = (ui, name) => nodes(ui.editor.tree, node => node.props.name === name)[0];

test('selection ignores foreign org/key URL parameters for org admins and validates numeric IDs', () => {
  const { model } = harness();
  assert.deepEqual(
    plain(model.resolveQuotaSelection(tenant, new URLSearchParams('organization_id=20&key_id=99'))),
    { organizationId: 10 },
  );
  for (const bad of ['-1', '1.5', 'Infinity', 'NaN', '9007199254740992', '0', ''])
    assert.equal(model.positiveId(bad), undefined);
  assert.deepEqual(
    plain(model.resolveQuotaSelection(admin, new URLSearchParams('organization_id=20&key_id=99'))),
    { organizationId: 20, keyId: 99 },
  );
});

test('cache keys isolate account, role, tenant and key; late responses cannot overwrite another tenant', async () => {
  const h = harness();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const a = deferred(),
    b = deferred();
  h.state.get = (_, { params }) => (params.organization_id === 10 ? a.promise : b.promise);
  const first = h.model.rateLimitQueryOptions(admin, 10);
  for (const changed of [
    { ...admin, id: 2 },
    { ...admin, role: 'org_admin' },
    { ...admin, organization_id: 20 },
  ])
    assert.notDeepEqual(
      plain(first.queryKey),
      plain(h.model.rateLimitQueryOptions(changed, 10).queryKey),
    );
  assert.notDeepEqual(
    plain(first.queryKey),
    plain(h.model.rateLimitQueryOptions(admin, 10, 5).queryKey),
  );
  const second = h.model.rateLimitQueryOptions(admin, 20);
  const pendingA = queryClient.fetchQuery(first),
    pendingB = queryClient.fetchQuery(second);
  b.resolve({ data: { data: snapshot(20, { daily_tokens: 500 }) } });
  await pendingB;
  a.resolve({ data: { data: snapshot(10, { daily_tokens: 0 }) } });
  await pendingA;
  assert.equal(queryClient.getQueryData(second.queryKey).organization.quotas.daily_tokens, 500);
  assert.equal(queryClient.getQueryData(first.queryKey).organization.quotas.daily_tokens, 0);
  queryClient.clear();
});

test('scoped query forbids foreign reads, validates response identity and propagates failures', async () => {
  const h = harness(tenant);
  for (const [user, org, key] of [
    [tenant, 20],
    [tenant, 10, 7],
    [{ ...tenant, role: 'key_viewer' }, 10],
  ]) {
    const options = h.model.rateLimitQueryOptions(user, org, key);
    assert.equal(options.enabled, false);
    await assert.rejects(options.queryFn({ signal: new AbortController().signal }), /scope/);
  }
  assert.equal(h.state.gets.length, 0);
  const options = h.model.rateLimitQueryOptions(tenant, 10);
  for (const data of [undefined, {}, snapshot(20), { organization: { organization_id: 10 } }]) {
    h.state.get = async () => ({ data: { data } });
    await assert.rejects(options.queryFn({ signal: new AbortController().signal }), /snapshot/);
  }
  const failure = new Error('Unavailable');
  h.state.get = async () => {
    throw failure;
  };
  await assert.rejects(options.queryFn({}), error => error === failure);
});

test('all six quota fields round-trip null, zero and positive values without amount rounding', () => {
  const { model } = harness();
  const quotas = {
    daily_tokens: 0,
    weekly_tokens: null,
    monthly_tokens: 1000000,
    daily_amount: 0.00000001,
    weekly_amount: 0,
    monthly_amount: 123.456789,
  };
  const values = model.organizationQuotaValues(snapshot(10, quotas).organization);
  const update = model.organizationQuotaUpdate(values).organizations[0];
  assert.equal(Object.keys(update).length, 8);
  for (const period of model.PERIODS)
    for (const metric of ['tokens', 'amount'])
      assert.equal(update[`${period}_quota_${metric}`], quotas[`${period}_${metric}`]);
  const clear = model.organizationQuotaUpdate({ organization_id: 10, qps: 0 }).organizations[0];
  for (const period of model.PERIODS)
    for (const metric of ['tokens', 'amount'])
      assert.equal(clear[`${period}_quota_${metric}`], null);
  for (const bad of [-1, NaN, Infinity, -Infinity, '12']) {
    assert.throws(
      () => model.organizationQuotaUpdate({ ...values, daily_quota_amount: bad }),
      /quota/,
    );
    assert.throws(
      () => model.organizationQuotaUpdate({ ...values, daily_quota_tokens: bad }),
      /quota/,
    );
  }
  for (const bad of [1.5, Number.MAX_SAFE_INTEGER + 1])
    assert.throws(
      () => model.organizationQuotaUpdate({ ...values, monthly_quota_tokens: bad }),
      /quota/,
    );
});

test('million-token presentation and input preserve raw token limits', () => {
  const h = harness();
  const model = h.model;
  assert.equal(model.formatMillionTokens(1_500_000, 'en-US'), '1.50M');
  assert.equal(model.formatMillionTokens(1_235_000, 'en-US'), '1.24M');
  assert.equal(model.formatMillionTokens(42, 'en-US'), '0.00M');
  assert.equal(model.millionTokenInputValue(1_500_001), 1.500001);
  assert.equal(model.tokensFromMillionInput(1.500001), 1_500_001);
  assert.equal(model.tokensFromMillionInput(0), 0);
  assert.equal(model.tokensFromMillionInput(null), null);
  assert.equal(model.quotaProgressPercent(1, 3), 33.33);
  assert.equal(model.quotaProgressPercent(5, 3), 100);
  assert.equal(model.quotaProgressPercent(0, 0), 100);
  const ui = h.page(snapshot(10, { daily_tokens: 1_500_001 }));
  const field = nodes(ui.editor.tree, node => node.props.name === 'daily_quota_tokens')[0];
  assert.equal(field.props.getValueProps(1_500_001).value, 1.500001);
  assert.equal(field.props.getValueFromEvent(1.500001), 1_500_001);
  assert.equal(field.props.children.props.addonAfter, 'M Token');
  assert.equal(field.props.children.props.precision, 6);
  assert.equal(form(ui, 'organization-limit').props.form.values.daily_quota_tokens, 1_500_001);
});

test('UTC+8 resets use midnight, Monday and calendar month across year and leap-day boundaries', () => {
  const { quotaResetAt } = harness().model;
  const cases = [
    ['daily', '2026-11-05T15:59:59Z', '2026-11-05T16:00:00.000Z'],
    ['daily', '2026-11-05T16:00:00Z', '2026-11-06T16:00:00.000Z'],
    ['weekly', '2026-11-08T15:59:59Z', '2026-11-08T16:00:00.000Z'],
    ['weekly', '2026-11-08T16:00:00Z', '2026-11-15T16:00:00.000Z'],
    ['monthly', '2026-12-31T15:59:59Z', '2026-12-31T16:00:00.000Z'],
    ['monthly', '2026-12-31T16:00:00Z', '2027-01-31T16:00:00.000Z'],
    ['daily', '2028-02-28T16:00:00Z', '2028-02-29T16:00:00.000Z'],
  ];
  for (const [period, now, expected] of cases)
    assert.equal(quotaResetAt(period, Date.parse(now)), expected);
});

test('organization form loads every field, sends explicit clears, and awaits scoped refresh after saving', async () => {
  const h = harness();
  const ui = h.page(snapshot(10, { daily_tokens: 5, daily_amount: 0.12345678 }));
  const organization = form(ui, 'organization-limit');
  assert.equal(organization.props.disabled, false);
  assert.equal(organization.props.form.values.daily_quota_tokens, 5);
  assert.equal(organization.props.form.values.weekly_quota_tokens, null);
  const response = deferred(),
    refresh = deferred();
  h.state.put = () => response.promise;
  h.state.invalidate = () => refresh.promise;
  const values = {
    ...organization.props.form.values,
    daily_quota_tokens: null,
    weekly_quota_tokens: 0,
  };
  const pending = organization.props.onFinish(values);
  await organization.props.onFinish(values); // stale handler cannot double-submit
  ui.editor.render();
  assert.equal(form(ui, 'organization-limit').props.disabled, true);
  assert.equal(h.state.puts.length, 1);
  assert.equal(h.state.puts[0][0], '/admin/rate-limits');
  const payload = h.state.puts[0][1].organizations[0];
  assert.equal(payload.daily_quota_tokens, null);
  assert.equal(payload.weekly_quota_tokens, 0);
  assert.equal(payload.monthly_quota_tokens, null);
  assert.equal(payload.daily_quota_amount, 0.12345678);
  response.resolve();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(plain(h.state.invalidations[0].queryKey), [
    'organization-rate-limits',
    1,
    'super_admin',
    10,
    10,
  ]);
  assert.equal(h.state.successes.length, 0);
  refresh.resolve();
  await pending;
  ui.editor.render();
  assert.equal(form(ui, 'organization-limit').props.disabled, false);
});

test('loading, failed refresh and mismatched organization block form saves; save failures retain edits', async () => {
  for (const [data, changes] of [
    [undefined, { isPending: true }],
    [snapshot(), { isFetching: true }],
    [snapshot(), { isError: true }],
  ]) {
    const h = harness();
    const ui = h.page(data, changes);
    const organization = form(ui, 'organization-limit');
    assert.equal(organization.props.disabled, true);
    await organization.props.onFinish({ organization_id: 10, qps: 1 });
    assert.equal(h.state.puts.length, 0);
  }
  const h = harness();
  const ui = h.page();
  const organization = form(ui, 'organization-limit');
  await organization.props.onFinish({ organization_id: 20, qps: 1 });
  assert.equal(h.state.puts.length, 0);
  organization.props.form.setFieldsValue({ daily_quota_amount: 1.23456789 });
  h.state.put = async () => {
    throw new Error('failed');
  };
  await organization.props.onFinish(organization.props.form.values);
  ui.editor.render();
  assert.equal(form(ui, 'organization-limit').props.form.values.daily_quota_amount, 1.23456789);
  assert.equal(h.state.errors.length, 1);
  assert.equal(h.state.invalidations.length, 0);
});

test('URL changes remount form state, clear key selection and do not reuse another tenant snapshot', () => {
  const h = harness(admin, 'organization_id=10&key_id=7');
  const ui = h.page(snapshot(10, { daily_tokens: 42 }));
  ui.selection.props.select(20);
  assert.equal(h.state.params.get('key_id'), null);
  const next = ui.scoped.render();
  assert.notEqual(next.key, ui.selection.key);
  const editor = h.mount(next.type, next.props);
  const organization = form({ editor }, 'organization-limit');
  assert.equal(organization.props.disabled, true);
  assert.equal(organization.props.form.values.daily_quota_tokens, undefined);
  assert.equal(nodes(editor.tree, node => node.type?.name === 'OrganizationQuotaUsage').length, 0);
  h.state.user = { ...admin, id: 99 };
  const otherAccount = h.load('features/ratelimits/RateLimitsPage.tsx').RateLimitsPage();
  assert.notEqual(ui.outer.key, otherAccount.key);
});

test('unmounted save completion only invalidates its original scope and never changes the new editor', async () => {
  const h = harness();
  const ui = h.page();
  const response = deferred();
  h.state.put = () => response.promise;
  const pending = form(ui, 'organization-limit').props.onFinish({ organization_id: 10, qps: 20 });
  ui.editor.unmount();
  ui.selection.props.select(20);
  const next = ui.scoped.render();
  const editor = h.mount(next.type, next.props);
  response.resolve();
  await pending;
  assert.equal(h.state.successes.length, 0);
  assert.equal(h.state.invalidations[0].queryKey.at(-1), 10);
  assert.equal(form({ editor }, 'organization-limit').props.form.values.organization_id, undefined);
});

test('org admin exposes only its organization read and no editing, key or global forms', async () => {
  const h = harness(tenant, 'organization_id=20&key_id=77');
  const ui = h.page();
  assert.equal(ui.selection.props.selectedOrgId, 10);
  assert.equal(ui.selection.props.selectedKeyId, undefined);
  assert.equal(nodes(ui.editor.tree, node => node.type === h.Form).length, 0);
  assert.equal(nodes(ui.editor.tree, node => node.props.htmlType === 'submit').length, 0);
  const selector = nodes(ui.editor.tree, node => node.type === 'Select')[0];
  assert.equal(selector.props.disabled, true);
  assert.deepEqual(plain(selector.props.options.map(option => option.value)), [10]);
  assert.ok(
    nodes(ui.editor.tree, node => node.type === 'Alert').some(
      node => node.props.message === 'ratelimits.org_readonly_hint',
    ),
  );
  const lookup = h.state.options.find(option => option.queryKey[0] === 'rate-limit-organizations');
  h.state.get = async () => ({ data: { data: { id: 10, name: 'Own organization' } } });
  assert.deepEqual(plain(await lookup.queryFn({ signal: new AbortController().signal })), [
    { id: 10, name: 'Own organization' },
  ]);
  assert.equal(h.state.gets[0][0], '/admin/organizations/10');
  assert.ok(
    h.state.options
      .filter(option => option.queryKey[0] === 'rate-limit-keys')
      .every(option => !option.enabled),
  );
});

test('key reads wait for membership and never replace the separate organization usage query', () => {
  const h = harness(admin, 'organization_id=10&key_id=7');
  const ui = h.page();
  let keyReads = h.state.options.filter(
    option => option.queryKey[0] === 'organization-rate-limits' && option.queryKey.at(-1) === 7,
  );
  assert.ok(keyReads.every(option => !option.enabled));
  h.respond({ queryKey: ['rate-limit-keys', ...h.model.quotaScope(admin), 10] }, [
    { id: 7, name: 'Owned key' },
  ]);
  h.respond(h.model.rateLimitQueryOptions(admin, 10, 7), {
    ...snapshot(),
    key: { key_id: 7, qps: 30 },
  });
  ui.editor.render();
  keyReads = h.state.options.filter(
    option => option.queryKey[0] === 'organization-rate-limits' && option.queryKey.at(-1) === 7,
  );
  assert.equal(keyReads.at(-1).enabled, true);
  assert.equal(form(ui, 'key-limit').props.form.values.key_id, 7);
  const usage = nodes(ui.editor.tree, node => node.type?.name === 'OrganizationQuotaUsage')[0];
  assert.equal(usage.props.organization.usage.daily_tokens, 42);
});

test('usage table distinguishes unlimited, blocked, fractional money and unknown usage with UTC+8 resets', () => {
  const h = harness();
  const { OrganizationQuotaUsage } = h.load('features/ratelimits/OrganizationQuotaUsage.tsx');
  const organization = snapshot(10, { daily_tokens: 0, daily_amount: 0.00000001 }).organization;
  const tree = OrganizationQuotaUsage({ organization });
  const table = nodes(tree, node => node.type === 'Table')[0];
  const [daily, weekly] = table.props.dataSource;
  assert.equal(daily.tokens, '42 / ratelimits.org_blocked');
  assert.equal(daily.amount, '$1e-8 / $1e-8');
  assert.equal(weekly.tokens, '80 / keys.unlimited');
  assert.equal(weekly.amount, 'ratelimits.org_usage_unavailable / keys.unlimited');
  assert.match(daily.reset, /Asia\/Shanghai/);
});

test('org admin rate-limit table displays tokens in millions without changing amount or admin view', () => {
  const h = harness(tenant);
  const ui = h.page(snapshot(10, { daily_tokens: 1_500_000, daily_amount: 2 }));
  const usage = nodes(ui.editor.tree, node => node.type?.name === 'OrganizationQuotaUsage')[0];
  assert.equal(usage.props.millionTokens, true);
  const { OrganizationQuotaUsage } = h.load('features/ratelimits/OrganizationQuotaUsage.tsx');
  const tree = OrganizationQuotaUsage(usage.props);
  const daily = nodes(tree, node => node.type === 'Table')[0].props.dataSource[0];
  assert.equal(daily.tokens, '0.00M / 1.50M');
  assert.equal(daily.amount, '$1e-8 / $2');
  const adminView = harness(admin).page(snapshot(10, { daily_tokens: 1_500_000 }));
  const adminUsage = nodes(
    adminView.editor.tree,
    node => node.type?.name === 'OrganizationQuotaUsage',
  )[0];
  assert.equal(adminUsage.props.millionTokens, false);
});

test('usage table prefers server reset timestamps and falls back only for missing periods', () => {
  const h = harness();
  const fallbackCalls = [];
  h.model.quotaResetAt = period => {
    fallbackCalls.push(period);
    return `local-${period}`;
  };
  const { OrganizationQuotaUsage } = h.load('features/ratelimits/OrganizationQuotaUsage.tsx');
  const resets = {
    daily: '2026-11-05T16:00:00Z',
    weekly: '2026-11-08T16:00:00Z',
    monthly: '2026-11-30T16:00:00Z',
  };
  for (const supplied of [resets, { daily: resets.daily }, undefined]) {
    fallbackCalls.length = 0;
    const organization = { ...snapshot().organization, resets: supplied };
    const tree = OrganizationQuotaUsage({ organization });
    const rows = nodes(tree, node => node.type === 'Table')[0].props.dataSource;
    for (const row of rows) {
      assert.equal(row.reset, `${supplied?.[row.period] ?? `local-${row.period}`} Asia/Shanghai`);
    }
    assert.deepEqual(
      fallbackCalls,
      ['daily', 'weekly', 'monthly'].filter(period => !supplied?.[period]),
    );
  }
});

test('home summary hides fabricated/stale metrics on initial or refresh failure and supports retry', async () => {
  for (const data of [undefined, snapshot()]) {
    const h = harness(tenant);
    let retried = false;
    h.respond(h.model.rateLimitQueryOptions(tenant, 10), data, {
      isError: true,
      isLoading: false,
      refetch: async () => {
        retried = true;
      },
    });
    const { OrganizationQuotaSummary } = h.load('features/home/OrganizationQuotaSummary.tsx');
    const tree = OrganizationQuotaSummary({ user: tenant });
    assert.equal(tree.props.extra.type.name, 'RefreshButton');
    assert.equal(nodes(tree, node => node.type?.name === 'PeriodRow').length, 0);
    assert.equal(nodes(tree, node => node.type === 'Alert').length, 1);
    await tree.props.extra.props.onClick();
    assert.equal(retried, true);
  }
});

test('home summary preserves usage for unlimited and blocked quotas; no request without organization', () => {
  const h = harness(tenant);
  h.respond(h.model.rateLimitQueryOptions(tenant, 10), snapshot(10, { daily_tokens: 0 }));
  const { OrganizationQuotaSummary } = h.load('features/home/OrganizationQuotaSummary.tsx');
  const tree = OrganizationQuotaSummary({ user: tenant });
  const rows = nodes(tree, node => node.type?.name === 'PeriodRow');
  assert.equal(rows.length, 6);
  const blocked = rows[0].type(rows[0].props);
  assert.match(textContent(blocked), /0.00M \/ ratelimits.org_blocked/);
  assert.equal(nodes(blocked, node => node.type === 'Progress')[0].props.status, 'exception');
  const unlimited = rows[2].type(rows[2].props);
  assert.match(textContent(unlimited), /0.00M \/ keys.unlimited/);
  assert.equal(OrganizationQuotaSummary({ user: { ...tenant, organization_id: 0 } }), null);
  assert.equal(h.state.options.at(-1).enabled, false);
});

test('a refreshed organization replaces all six form values, including fields cleared on the server', () => {
  const h = harness();
  const ui = h.page(
    snapshot(10, {
      daily_tokens: 100,
      weekly_tokens: 200,
      monthly_tokens: 300,
      daily_amount: 1,
      weekly_amount: 2,
      monthly_amount: 3,
    }),
  );
  h.respond(h.model.rateLimitQueryOptions(admin, 10), snapshot(10, { daily_tokens: 0 }), {
    dataUpdatedAt: 2,
  });
  ui.editor.render();
  const values = form(ui, 'organization-limit').props.form.values;
  assert.equal(values.daily_quota_tokens, 0);
  for (const field of [
    'weekly_quota_tokens',
    'monthly_quota_tokens',
    'daily_quota_amount',
    'weekly_quota_amount',
    'monthly_quota_amount',
  ])
    assert.equal(values[field], null);
  const fields = nodes(
    ui.editor.tree,
    node => typeof node.props.name === 'string' && /_(tokens|amount)$/.test(node.props.name),
  );
  assert.equal(fields.length, 6);
  assert.ok(fields.every(field => field.props.rules?.[0].validator));
});

test('an unknown key deep link clears only the key after organization membership loads', () => {
  const h = harness(admin, 'organization_id=10&key_id=77');
  const ui = h.page();
  h.respond({ queryKey: ['rate-limit-keys', ...h.model.quotaScope(admin), 10] }, [
    { id: 7, name: 'Owned key' },
  ]);
  ui.editor.render();
  assert.equal(h.state.params.get('organization_id'), '10');
  assert.equal(h.state.params.get('key_id'), null);
  assert.ok(
    h.state.options
      .filter(option => option.queryKey.at(-1) === 77)
      .every(option => !option.enabled),
  );
});
