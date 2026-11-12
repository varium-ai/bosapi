const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-overview-test-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/app/navigation.ts',
      'src/features/home/overview.ts',
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
  throw new Error(error.stdout?.toString() || error.message);
}
after(() => fs.rmSync(temporary, { recursive: true, force: true }));

const { getNavigationGroups, getSelectedNavigationPath } = require(
  path.join(temporary, 'app/navigation.js'),
);
const {
  getUsageRange,
  parseUsagePoints,
  parseRuntimeStatus,
  parseRecentRequests,
  sumUsage,
  getErrorRate,
  runtimeNeedsAttention,
  getTrendGeometry,
} = require(path.join(temporary, 'features/home/overview.js'));
const { enUS } = require(path.join(temporary, 'i18n/locales/en-US.js'));
const { zhCN } = require(path.join(temporary, 'i18n/locales/zh-CN.js'));
const paths = role =>
  getNavigationGroups(role).flatMap(group => group.items.map(item => item.path));

test('admin navigation has five task groups and consolidated routes exactly once', () => {
  const groups = getNavigationGroups('super_admin');
  assert.deepEqual(
    groups.map(group => group.key),
    ['workbench', 'resources', 'monitoring', 'security', 'settings'],
  );
  const expectedPaths = [
    '/',
    '/admin/providers',
    '/admin/organizations',
    '/admin/keys',
    '/admin/usage',
    '/admin/requests',
    '/admin/status',
    '/admin/security-events',
    '/admin/rate-limits',
    '/admin/admin-users',
    '/admin/announcements',
    '/admin/api-address',
    '/admin/message-config',
    '/admin/usage-reporting',
  ];
  assert.deepEqual(paths('super_admin'), expectedPaths);
  assert.equal(new Set(paths('super_admin')).size, expectedPaths.length);
  const routes = fs.readFileSync(path.join(root, 'src/app/routes.tsx'), 'utf8');
  for (const route of paths('super_admin').filter(route => route !== '/')) {
    assert.ok(routes.includes(`path="${route.slice(1)}"`), route);
  }
});

test('community navigation excludes online system updates for every role', () => {
  for (const role of ['super_admin', 'org_admin', 'key_viewer']) {
    assert.ok(!paths(role).includes('/admin/system-updates'), role);
  }
});

test('organization navigation excludes platform-only endpoints without empty groups', () => {
  const routes = paths('org_admin');
  for (const forbidden of [
    '/admin/status',
    '/admin/reports',
    '/admin/system-updates',
    '/admin/usage-reporting',
    '/admin/message-config',
  ]) {
    assert.ok(!routes.includes(forbidden), forbidden);
  }
  assert.ok(routes.includes('/admin/announcements'));
  assert.ok(
    getNavigationGroups('org_admin')
      .find(group => group.key === 'settings')
      .items.some(item => item.path === '/admin/announcements'),
  );
  assert.ok(routes.includes('/admin/providers'));
  assert.ok(routes.includes('/admin/usage'));
  assert.ok(getNavigationGroups('org_admin').every(group => group.items.length > 0));
});

test('key viewer navigation contains only key portal routes and nested key detail stays selected', () => {
  assert.ok(paths('key_viewer').every(route => route.startsWith('/key-portal')));
  assert.equal(paths('key_viewer').length, 5);
  assert.equal(getSelectedNavigationPath('/admin/keys/42'), '/admin/keys');
  assert.equal(getSelectedNavigationPath('/admin/usage'), '/admin/usage');
});

test('usage windows use UTC calendar filters, including month and year boundaries', () => {
  assert.deepEqual(getUsageRange('7d', new Date('2026-10-13T00:30:00Z')), {
    from: '2026-10-07',
    to: '2026-10-13',
  });
  assert.deepEqual(getUsageRange('30d', new Date('2026-01-02T00:00:00Z')), {
    from: '2025-12-04',
    to: '2026-01-02',
  });
  assert.deepEqual(getUsageRange('total'), {});
});

const point = overrides => ({
  period: '2026-10-12',
  requests: 10,
  errors: 2,
  input_tokens: 100,
  output_tokens: 50,
  total_tokens: 150,
  cost_amount: 0.25,
  ...overrides,
});
test('metrics sum exact existing counters and derive only HTTP error rate', () => {
  const summary = sumUsage(
    parseUsagePoints({ items: [point(), point({ requests: 30, errors: 0 })] }),
  );
  assert.deepEqual(summary, {
    requests: 40,
    errors: 2,
    input_tokens: 200,
    output_tokens: 100,
    total_tokens: 300,
    cost_amount: 0.5,
  });
  assert.equal(getErrorRate(summary), 5);
});

test('empty successful usage is zero, but absent or invalid responses never become zero', () => {
  const summary = sumUsage(parseUsagePoints({ items: [] }));
  assert.equal(summary.requests, 0);
  assert.equal(getErrorRate(summary), null);
  for (const invalid of [
    undefined,
    {},
    { items: null },
    { items: [point({ requests: undefined })] },
    { items: [point({ total_tokens: -1 })] },
    { items: [point({ errors: NaN })] },
  ]) {
    assert.throws(() => parseUsagePoints(invalid));
  }
});

const status = {
  version: 'development',
  uptime_seconds: 4200,
  mysql: { status: 'ok', latency_ms: 2 },
  redis: { status: 'not_configured' },
  queue: { state: 'running' },
  draining: false,
};
test('runtime distinguishes an optional unconfigured cache from a failed or unknown subsystem', () => {
  assert.equal(runtimeNeedsAttention(parseRuntimeStatus(status)), false);
  for (const invalid of [{}, { ...status, uptime_seconds: NaN }, { ...status, mysql: {} }])
    assert.throws(() => parseRuntimeStatus(invalid));
  assert.equal(
    runtimeNeedsAttention({ ...status, mysql: { status: 'unavailable', latency_ms: 5 } }),
    true,
  );
  assert.equal(runtimeNeedsAttention({ ...status, redis: { status: 'unavailable' } }), true);
  assert.equal(runtimeNeedsAttention({ ...status, redis: { status: 'unknown' } }), true);
  assert.equal(runtimeNeedsAttention({ ...status, queue: { state: 'stopped' } }), true);
  assert.equal(runtimeNeedsAttention({ ...status, draining: true }), true);
});

test('request trend is stable for empty, zero and single bucket data without mutating input', () => {
  assert.equal(getTrendGeometry([]).line, '');
  assert.ok(Number.isFinite(getTrendGeometry([point({ requests: 0 })]).points[0].y));
  assert.equal(getTrendGeometry([point()]).points[0].x, 320);
  const points = [point({ period: '2026-10-12' }), point({ period: '2026-10-11' })];
  assert.equal(getTrendGeometry(points).points[0].period, '2026-10-11');
  assert.equal(points[0].period, '2026-10-12');
});

test('request trend fills narrow and wide containers while retaining the same measurements', () => {
  const points = [
    point({ period: '2026-10-16', requests: 3 }),
    point({ period: '2026-10-19', requests: 6 }),
    point({ period: '2026-10-20', requests: 11 }),
  ];
  for (const width of [240, 640, 1440]) {
    const {
      points: plotted,
      plot,
      maximum,
      area,
    } = getTrendGeometry(points, {
      width,
      height: 200,
    });
    assert.equal(plotted[0].x, plot.left);
    assert.equal(plotted.at(-1).x, width - 20);
    assert.equal(plotted[1].x, (plot.left + plot.right) / 2);
    assert.equal(maximum, 11);
    assert.deepEqual(
      plotted.map(({ period, requests }) => ({ period, requests })),
      points.map(({ period, requests }) => ({ period, requests })),
    );
    assert.ok(plotted.every(({ y }) => y >= plot.top && y <= plot.bottom));
    assert.ok(area.endsWith(`${plot.right},${plot.bottom}`));
    const single = getTrendGeometry([points[0]], { width, height: 200 });
    assert.equal(single.points[0].x, width / 2);
    assert.equal(single.area, '');
  }
});

test('overview only calls registered existing APIs and protects platform telemetry by role', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/home/HomePage.tsx'), 'utf8');
  assert.doesNotMatch(
    source,
    /curl|QRCode|integration_title|auth_headers_title|<CodeBlock|Math\.random/,
  );
  assert.match(source, /superAdmin && <SystemOverview/);
  assert.match(source, /superAdmin\s*\?\s*\[\s*\{\s*key: 'providers'/);
  assert.match(source, /validateStatus: status => status === 503/);
  assert.match(source, /range === 'total' \? '30d' : range/);
  const backend = path.join(root, '..', 'backend/internal/admin/interfaces');
  for (const [file, routes] of Object.entries({
    'usage_http.go': ['/usage/total', '/usage/daily', '/usage'],
    'status_http.go': ['/status'],
    'organizations_http.go': ['/organizations'],
    'api_keys_http.go': ['/keys'],
    'providers_http.go': ['/providers'],
    'security_events_http.go': ['/security-events'],
  })) {
    const registered = fs.readFileSync(path.join(backend, file), 'utf8');
    for (const route of routes) {
      assert.ok(registered.includes(`GET("${route}"`), route);
      assert.ok(source.includes(`'/admin${route}'`), route);
    }
  }
});

test('overview localizations and navigation group labels have complete parity', () => {
  assert.deepEqual(Object.keys(enUS.overview).sort(), Object.keys(zhCN.overview).sort());
  for (const group of [
    ...getNavigationGroups('super_admin'),
    ...getNavigationGroups('key_viewer'),
  ]) {
    for (const locale of [enUS, zhCN])
      assert.ok(
        group.label.split('.').reduce((obj, key) => obj[key], locale),
        group.label,
      );
  }
});

test('connected preview forwards real requests with TLS verification and contains no API fixtures', () => {
  const source = fs.readFileSync(path.join(root, 'scripts/preview-connected.mjs'), 'utf8');
  assert.match(source, /secure: true/);
  assert.doesNotMatch(source, /profiles|ui-preview-|res\.end|middlewares|localStorage/);
});

test('recent requests accept cursor pages with null total and reject malformed results', () => {
  const item = {
    id: 42,
    request_id: 'request-42',
    model: 'example',
    status_code: 200,
    total_tokens: 12,
    created_at: '2026-11-02T01:00:00Z',
  };
  assert.deepEqual(
    parseRecentRequests({ items: [item], total: null, has_more: true, next_cursor: 'cursor' }),
    [item],
  );
  assert.deepEqual(
    parseRecentRequests({ items: [], total: null, has_more: false, next_cursor: '' }),
    [],
  );
  for (const payload of [undefined, {}, { items: null }, { items: [null] }, { items: [{}] }]) {
    assert.throws(() => parseRecentRequests(payload));
  }
});
