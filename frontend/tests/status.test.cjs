const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-status-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/status/telemetry.ts',
      'src/i18n/monitorMessages.ts',
      '--rootDir',
      'src',
      '--outDir',
      temp,
      '--module',
      'commonjs',
      '--target',
      'es2020',
      '--skipLibCheck',
      '--strict',
    ],
    { cwd: root },
  );
} catch (e) {
  fs.rmSync(temp, { recursive: true, force: true });
  throw e;
}
after(() => fs.rmSync(temp, { recursive: true, force: true }));
const {
  bytes,
  chartSegments,
  isStale,
  parseTelemetry,
  percent,
  rollingErrorRate,
  selectHistory,
  warnings,
} = require(path.join(temp, 'features/status/telemetry.js'));
const { monitorEnglish, monitorChinese } = require(path.join(temp, 'i18n/monitorMessages.js'));
const now = Date.now();
const at = new Date(now).toISOString();
const snapshot = () => ({
  sampled_at: at,
  mysql: { status: 'ok' },
  redis: { status: 'ok' },
  queue: { state: 'running' },
  nodes: [],
  certificates: [],
  history: [],
});
test('unknown values are never converted to zero measurements', () => {
  for (const value of [null, undefined, NaN, '0']) {
    assert.equal(bytes(value), '—');
    assert.equal(percent(value), '—');
  }
  assert.equal(bytes(0), '0 B');
  assert.equal(percent(0), '0.0%');
  assert.equal(bytes(0.1), '0 B');
});
test('old backends remain recognizable without invented nodes', () => {
  const old = parseTelemetry({
    mysql: { status: 'ok' },
    redis: { status: 'not_configured' },
    queue: { state: 'running' },
  });
  assert.deepEqual(old.nodes, []);
  assert.equal(old.sampled_at, undefined);
  assert.throws(() => parseTelemetry({}));
});
test('stale and future-clock samples do not count as healthy', () => {
  assert.ok(isStale(undefined, now));
  assert.ok(isStale(new Date(now - 91000).toISOString(), now));
  assert.ok(isStale(new Date(now + 31000).toISOString(), now));
  assert.equal(isStale(at, now), false);
});
test('charts retain missing data gaps and reject invalid time', () => {
  assert.deepEqual(chartSegments([], 'cpu'), []);
  assert.deepEqual(chartSegments([{ at: 'invalid', cpu: 4 }], 'cpu'), []);
  const points = [0, 30000, 60000].map((delta, i) => ({
    at: new Date(now + delta).toISOString(),
    cpu: i === 1 ? null : 0,
  }));
  const segments = chartSegments(points, 'cpu');
  assert.equal(segments.length, 2);
  assert.ok(!segments.join().includes('NaN'));
});
test('warnings distinguish degraded services, absent telemetry and real thresholds', () => {
  assert.deepEqual(warnings(snapshot(), now), []);
  const data = snapshot();
  data.redis = { status: 'unavailable' };
  data.nodes = [
    {
      id: 'n1',
      status: 'ok',
      sampled_at: at,
      cpu_percent: 95,
      memory: { used_percent: 93 },
      disks: [{ path: '/', used_percent: 86 }],
    },
  ];
  data.certificates = [{ target: 'test:443', status: 'expiring', checked_at: at }];
  assert.deepEqual(
    warnings(data, now).map(x => x.key),
    ['subsystem_warning', 'cpu_warning', 'memory_warning', 'disk_warning', 'certificate_warning'],
  );
});
test('stale nodes do not generate current utilization warnings', () => {
  const data = snapshot();
  data.nodes = [{ id: 'n', sampled_at: new Date(now - 120000).toISOString(), cpu_percent: 99 }];
  assert.deepEqual(
    warnings(data, now).map(x => x.key),
    ['node_stale'],
  );
});
test('HTTP warning uses the rolling request count, not the last sample count', () => {
  const data = snapshot();
  data.traffic = {
    error_window_seconds: 300,
    error_window_requests: 20,
    error_window_errors: 1,
    requests: 0,
    error_rate: 5,
  };
  assert.equal(rollingErrorRate(data.traffic), 5);
  assert.deepEqual(
    warnings(data, now).map(x => x.key),
    ['error_warning'],
  );
  data.traffic.error_window_requests = 19;
  data.traffic.requests = 100;
  assert.deepEqual(warnings(data, now), []);
  data.traffic.error_window_requests = 20;
  data.traffic.error_rate = 4.9;
  assert.deepEqual(warnings(data, now), []);
});
test('old, empty and malformed HTTP metrics never claim a rolling measurement', () => {
  for (const traffic of [
    undefined,
    { requests: 100, error_rate: 64.3 },
    { error_window_seconds: 30, error_window_requests: 100, error_rate: 64.3 },
    { error_window_seconds: 300, error_window_requests: 0, error_rate: 0 },
    { error_window_seconds: 300, error_window_requests: 20, error_rate: null },
    { error_window_seconds: 300, error_window_requests: 20, error_rate: 101 },
  ]) {
    assert.equal(rollingErrorRate(traffic), undefined);
    assert.deepEqual(warnings({ ...snapshot(), traffic }, now), []);
  }
  assert.equal(
    rollingErrorRate({ error_window_seconds: 300, error_window_requests: 20, error_rate: 0 }),
    0,
  );
});
test('process CPU retains multi-core utilization and charts preserve missing baselines', () => {
  assert.equal(percent(250), '250.0%');
  const points = [
    { at, cpu_percent: null, rss_bytes: 1024 },
    { at: new Date(now + 30000).toISOString(), cpu_percent: 250, rss_bytes: 2048 },
  ];
  assert.equal(chartSegments(points, 'cpu_percent').length, 1);
  assert.equal(bytes(points[1].rss_bytes), '2.0 KiB');
  const source = fs.readFileSync(path.join(root, 'src/features/status/StatusPage.tsx'), 'utf8');
  assert.match(source, /percent\(process.cpu_percent\)/);
  assert.match(source, /bytes\(process.rss_bytes\)/);
  assert.match(source, /percent\(process.memory_percent\)/);
  assert.match(source, /metric="cpu_percent"/);
  assert.match(source, /metric="rss_bytes"/);
});
test('partial middleware probes and unreadable node snapshots require attention', () => {
  const data = snapshot();
  data.mysql.details_status = 'unavailable';
  data.redis.details_status = 'unavailable';
  data.node_source = 'unavailable';
  assert.deepEqual(
    warnings(data, now).map(x => x.key),
    ['details_warning', 'details_warning', 'node_source_warning'],
  );
});
test('localizations remain complete', () =>
  assert.deepEqual(Object.keys(monitorEnglish), Object.keys(monitorChinese)));
test('monitor tabs retain nodes and middleware without a TLS certificate tab', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/status/StatusPage.tsx'), 'utf8');
  assert.match(source, /key: 'nodes'/);
  assert.match(source, /key: 'middleware'/);
  assert.doesNotMatch(source, /key: 'certificates'|data\.certificates|label\('certificates'\)/);
});
test('history selection uses elapsed time rather than sample count and never mutates input', () => {
  const points = [0, -60000, -910000, 60000].map(delta => ({
    at: new Date(now + delta).toISOString(),
    cpu: 1,
  }));
  const before = JSON.stringify(points);
  assert.equal(selectHistory(points, 15, now).length, 2);
  assert.equal(selectHistory(points, 60, now).length, 3);
  assert.equal(selectHistory(points, 15, now + 3600000).length, 0);
  assert.equal(JSON.stringify(points), before);
});
test('polling is bounded, permission-aware, and preserves degraded 503 data', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/status/StatusPage.tsx'), 'utf8');
  assert.match(source, /refetchIntervalInBackground: false/);
  assert.match(source, /auto \? 30000 : false/);
  assert.match(source, /user\?\.role === 'super_admin'/);
  assert.match(source, /code === 503/);
  assert.match(source, /refetchOnWindowFocus: auto,\s+refetchOnReconnect: auto/);
  assert.match(source, /globalThis\.setInterval\(tick, 10000\)/);
  assert.match(source, /globalThis\.clearInterval\(timer\)/);
  assert.match(source, /!legacy && !stale && !alerts.length/);
  assert.doesNotMatch(source, /Math\.random|setInterval.*refetch/);
});
