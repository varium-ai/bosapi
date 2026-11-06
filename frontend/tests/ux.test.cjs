const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-ux-test-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/common/ux.ts',
      'src/features/requestlogs/diagnostics.ts',
      'src/features/common/display.ts',
      'src/i18n/formatters.ts',
      'src/app/navigation.ts',
      'src/i18n/uxMessages.ts',
      'src/i18n/usageReportingMessages.ts',
      'src/features/settings/installationTelemetry.ts',
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
const { actionKey, eventKey, stepKey, validRequestID, routeTarget } = require(
  path.join(temporary, 'features/common/ux.js'),
);
const { getSelectedNavigationPath, getNavigationGroups } = require(
  path.join(temporary, 'app/navigation.js'),
);
const { uxEnglish, uxChinese } = require(path.join(temporary, 'i18n/uxMessages.js'));
const { usageReportingEnglish, usageReportingChinese } = require(
  path.join(temporary, 'i18n/usageReportingMessages.js'),
);
const { parseInstallationPreview } = require(
  path.join(temporary, 'features/settings/installationTelemetry.js'),
);

test('actions distinguish removing tool calls from denying requests', () => {
  assert.equal(actionKey('observe', 'tool_call'), 'ux.observe');
  assert.equal(actionKey('block', 'tool_call'), 'ux.tool_block');
  for (const type of ['authentication', 'access_policy', 'rate_limit', 'quota'])
    assert.equal(actionKey('block', type), 'ux.request_block');
  assert.equal(actionKey('block', 'future_type'), 'ux.block');
  assert.equal(actionKey('future_action'), '');
});
test('unknown events and steps retain raw fallback instead of invented diagnosis', () => {
  assert.equal(eventKey('model_not_allowed'), 'ux.access_denied');
  assert.equal(eventKey('unknown'), '');
  assert.equal(stepKey('gateway.model.provider.checked'), 'checking_provider');
  assert.equal(stepKey('new.runtime.step'), '');
});
test('request ID validation exactly matches server character and length limits', () => {
  const backend = fs.readFileSync(
    path.join(root, '../backend/internal/platform/httpapi/http.go'),
    'utf8',
  );
  assert.ok(backend.includes('^[A-Za-z0-9._-]{1,128}$'));
  for (const value of ['a', 'client.request-01_A', 'a'.repeat(128)])
    assert.ok(validRequestID(value), value);
  for (const value of ['', 'a'.repeat(129), 'a b', '../x/y', '\u4e2d\u6587', 'x?y=z'])
    assert.equal(validRequestID(value), false, value);
});
test('route preview has explicit missing targets and does not invent provider names', () => {
  const providers = [{ id: 1, name: 'A' }];
  assert.equal(routeTarget(undefined, 'm', 'public', providers, 'missing'), 'missing');
  assert.equal(routeTarget(1, ' m ', 'public', providers, 'missing'), 'A / m');
  assert.equal(routeTarget(2, '', 'public', providers, 'missing'), 'missing / public');
});
test('old deep links select consolidated navigation without dropping routes', () => {
  assert.equal(getSelectedNavigationPath('/admin/errors'), '/admin/requests');
  assert.equal(getSelectedNavigationPath('/admin/request-logs'), '/admin/requests');
  assert.equal(getSelectedNavigationPath('/admin/reports'), '/admin/reports');
  assert.equal(getSelectedNavigationPath('/admin/keys/7'), '/admin/keys');
});
test('only super administrators can navigate to API address settings', () => {
  for (const role of ['super_admin', 'org_admin', 'key_viewer']) {
    const paths = getNavigationGroups(role).flatMap(group => group.items.map(item => item.path));
    assert.equal(paths.includes('/admin/api-address'), role === 'super_admin');
  }
});
test('new UX translations have complete parity and no empty messages', () => {
  assert.deepEqual(Object.keys(uxEnglish).sort(), Object.keys(uxChinese).sort());
  for (const locale of [uxEnglish, uxChinese])
    for (const value of Object.values(locale)) assert.ok(value.trim());
});
test('inactive key detail history is not fetched and rate-limit writes retain role guards', () => {
  const detail = fs.readFileSync(path.join(root, 'src/features/keys/KeyDetailPage.tsx'), 'utf8');
  assert.equal(
    (detail.match(/!!keyData && section === 'activity' && activeTab ===/g) || []).length,
    4,
  );
  const limits = fs.readFileSync(
    path.join(root, 'src/features/ratelimits/RateLimitsPage.tsx'),
    'utf8',
  );
  assert.match(
    limits,
    /if \(!superAdmin \|\| loading \|\| saving \|\| !snapshot \|\| loadError\) return/,
  );
  assert.match(limits, /Number\(values.key_id\) !== selectedKeyId/);
  assert.match(limits, /const \[loading, setLoading\] = useState\(true\)/);
  assert.doesNotMatch(limits, /setSnapshot\(null\)/);
  assert.doesNotMatch(limits, /disabled=\{[^}]*\bloading\b/);
  assert.match(limits, /loading=\{savingScope === 'global'\}/);
  assert.match(limits, /loading=\{savingScope === 'org'\}/);
  assert.match(limits, /className="rate-limits-status-slot"/);
  assert.doesNotMatch(limits, /keys\.some\(key => !key\.key_suffix\)/);
  assert.doesNotMatch(limits, /t\('common\.loading'/);
});

test('advanced fields stay mounted and destructive confirmation remains explicit', () => {
  const shared = fs.readFileSync(path.join(root, 'src/features/common/ConsoleUX.tsx'), 'utf8');
  assert.match(shared, /forceRender: true/);
  assert.match(shared, /okButtonProps: \{ danger: true \}/);
});

test('console actions use the neutral palette while semantic feedback keeps named colors', () => {
  const theme = fs.readFileSync(path.join(root, 'src/theme/consoleTheme.ts'), 'utf8');
  const locale = fs.readFileSync(path.join(root, 'src/i18n/LocaleSettingsControl.tsx'), 'utf8');
  const users = fs.readFileSync(
    path.join(root, 'src/features/adminusers/AdminUsersPage.tsx'),
    'utf8',
  );
  assert.match(theme, /colorLink: p\.gray,\s+colorLinkHover: p\.ink,\s+colorLinkActive: p\.ink/);
  assert.doesNotMatch(locale, /#1677ff|#1890ff/);
  assert.doesNotMatch(users, /#1677ff|#1890ff/);
});

test('entry reuses its React root when translation updates invalidate the module', () => {
  const source = fs.readFileSync(path.join(root, 'src/main.tsx'), 'utf8');
  assert.match(source, /import\.meta\.hot\?\.data\.root\s*\?\?\s*ReactDOM\.createRoot/);
  assert.match(source, /import\.meta\.hot\.data\.root = root/);
  assert.match(source, /BrowserRouter future=\{\{ v7_startTransition: true \}\}/);
  assert.match(source, /ConfigProvider\.config\(\{/);
  assert.match(source, /holderRender: children =>/);
});

test('startup loads only the active language resources', () => {
  const context = fs.readFileSync(path.join(root, 'src/i18n/I18nContext.tsx'), 'utf8');
  const resources = fs.readFileSync(path.join(root, 'src/i18n/resources.ts'), 'utf8');
  assert.doesNotMatch(context, /\.\/locales\/(?:zh-CN|en-US)|antd\/locale\/(?:zh_CN|en_US)/);
  assert.match(context, /requestedLanguage\.current !== lang/);
  assert.match(resources, /import\('\.\/locales\/zh-CN'\)/);
  assert.match(resources, /import\('\.\/locales\/en-US'\)/);
  assert.match(resources, /import\('antd\/locale\/zh_CN'\)/);
  assert.match(resources, /import\('antd\/locale\/en_US'\)/);
});

test('administrator list keeps the heavy table on the lazy login-record path', () => {
  const users = fs.readFileSync(
    path.join(root, 'src/features/adminusers/AdminUsersPage.tsx'),
    'utf8',
  );
  const records = fs.readFileSync(
    path.join(root, 'src/features/adminusers/AdminLoginRecordsModal.tsx'),
    'utf8',
  );
  assert.doesNotMatch(users, /\bTable\b/);
  assert.match(users, /lazy\(loadLoginRecordsModal\)/);
  assert.match(users, /const actionColumnWidth = language === 'zh-CN' \? 230 : 285/);
  assert.match(users, /width: actionColumnWidth/);
  assert.match(records, /import \{ Modal, Table, Tag, Typography \} from 'antd'/);
});

test('production cold-start plugin preloads route chunks and emits compressed assets', () => {
  const plugin = fs.readFileSync(path.join(root, 'coldStartPlugin.ts'), 'utf8');
  const vite = fs.readFileSync(path.join(root, 'vite.config.ts'), 'utf8');
  assert.match(vite, /coldStartPlugin\(\)/);
  assert.match(plugin, /e\.rel='modulepreload'/);
  assert.match(plugin, /writeFile\(`\$\{file\}\.gz`/);
  assert.match(plugin, /writeFile\(`\$\{file\}\.br`/);
  assert.match(plugin, /BROTLI_PARAM_QUALITY/);
});

const { compactKey, usageWithLimit } = require(path.join(temporary, 'features/common/display.js'));
const { formatCurrency } = require(path.join(temporary, 'i18n/formatters.js'));
test('masked keys expose only the first and last three known characters', () => {
  assert.equal(compactKey({ key_prefix: 'sk-gw-123456789', key_suffix: 'abcd' }), 'sk-...bcd'); // gitleaks:allow -- Synthetic test credential.
  assert.equal(compactKey({ key_prefix: 'sk-gw-123456789' }), 'sk-...'); // gitleaks:allow -- Synthetic test credential.
  assert.equal(compactKey({ masked_key: 'sk-gw-1234••••abcd' }), 'sk-...bcd');
  assert.equal(compactKey({ masked_key: 'sk-gw-1234••••' }), 'sk-...');
  assert.equal(compactKey({ masked_key: 'sk-123456' }), 'sk-...');
});
test('unlimited usage omits the denominator and preserves an explicit zero limit', () => {
  assert.equal(usageWithLimit('123', null, String), '123');
  assert.equal(usageWithLimit('123', undefined, String), '123');
  assert.equal(usageWithLimit('123', 0, String), '123 / 0');
  assert.equal(usageWithLimit('123', 500, String), '123 / 500');
});
test('USD uses a dollar symbol in both UI languages without the US prefix', () => {
  for (const locale of ['zh-CN', 'en-US']) assert.equal(formatCurrency(1.2345, locale), '$1.2345');
});
test('resource names use bounded cached reads and organization links search by name', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/common/ResourceName.tsx'), 'utf8');
  const lookup = fs.readFileSync(
    path.join(root, 'src/features/common/resourceNameLookup.ts'),
    'utf8',
  );
  assert.match(source, /createResourceNameLookup\(apiClient\)/);
  assert.match(lookup, /active >= 4/);
  assert.match(source, /staleTime: 5 \* 60 \* 1000/);
  assert.match(source, /encodeURIComponent\(label\)/);
  assert.doesNotMatch(source, /\/reveal|apiClient\.(post|put|delete)/);
});
test('navigation warms route data and usage rows reuse names from the list response', () => {
  const layout = fs.readFileSync(path.join(root, 'src/app/Layout.tsx'), 'utf8');
  const entry = fs.readFileSync(path.join(root, 'src/main.tsx'), 'utf8');
  const usage = fs.readFileSync(path.join(root, 'src/features/usage/UsagePage.tsx'), 'utf8');
  assert.match(layout, /onMouseEnter: \(\) => warmNavigationPath\(item\.path\)/);
  assert.match(layout, /prefetchQuery\(organizationOptionsQueryOptions\(\)\)/);
  assert.match(layout, /prefetchQuery\(adminKeysQueryOptions\(\)\)/);
  assert.match(layout, /prefetchQuery\(keyNameOptionsQueryOptions\(\)\)/);
  assert.match(entry, /staleTime: 30_000/);
  assert.match(usage, /orgName=\{r\.organization_name\}/);
  assert.match(usage, /keyName=\{r\.api_key_name\}/);
  assert.match(usage, /name=\{record\.api_key_name\}/);
});
test('model lists use a dialog rather than expanding table row height', () => {
  const source = fs.readFileSync(
    path.join(root, 'src/features/organizations/OrganizationsPage.tsx'),
    'utf8',
  );
  assert.doesNotMatch(source, /<details>/);
  assert.match(source, /className="model-list-dialog"/);
});

const { logFields, analyzeTrace, entryDiagnosis } = require(
  path.join(temporary, 'features/requestlogs/diagnostics.js'),
);
const traceRow = (event, message, level = 'INFO') => ({
  event,
  message,
  level,
  created_at: '2026-10-15T06:54:12Z',
});
test('historical screenshot cancellation is explained without inventing client disconnection or upstream status', () => {
  const rows = [
    traceRow(
      'gateway.rate_limit.check',
      'method=POST path=chat/completions model=deepseek-v4-pro operation=',
    ),
    traceRow(
      'gateway.upstream.request_failed',
      'provider_id=1 path=/chat/completions err=Post "https://api.example.com/v1/chat/completions": context canceled',
      'ERROR',
    ),
    traceRow(
      'gateway.request.completed',
      'status=502 duration_ms=20299 provider_id=1 model=deepseek-v4-pro',
    ),
  ];
  const result = analyzeTrace(rows);
  assert.equal(result.diagnosis, 'request_canceled');
  assert.equal(result.fields.status, '502');
  assert.equal(result.fields.duration_ms, '20299');
  assert.equal(result.fields.upstream_status, undefined);
  assert.equal(result.fields.phase, undefined);
  assert.equal(result.fields.model, 'deepseek-v4-pro');
});
test('quoted diagnostic fields preserve spaces and do not parse fields embedded inside error text', () => {
  const fields = logFields(
    'provider_name="My channel" model="test" err="failed model=untrusted" timeout_ms=0 stream=false operation=',
  );
  assert.equal(fields.provider_name, 'My channel');
  assert.equal(fields.model, 'test');
  assert.equal(fields.err, 'failed model=untrusted');
  assert.equal(fields.timeout_ms, '0');
  assert.equal(fields.stream, 'false');
});
test('transport evidence takes precedence over generic gateway errors and distinguishes timeout cancellation', () => {
  const rows = [
    traceRow(
      'gateway.upstream.request_failed',
      'reason=timeout phase=wait_headers err="context canceled"',
      'ERROR',
    ),
    traceRow(
      'gateway.request.completed',
      'status=504 error_code=upstream_timeout duration_ms=30000',
      'ERROR',
    ),
  ];
  assert.equal(analyzeTrace(rows).diagnosis, 'timeout');
  assert.equal(analyzeTrace(rows).fields.phase, 'wait_headers');
});
test('retry transport evidence is not mixed with an earlier upstream HTTP status or request ID', () => {
  const rows = [
    traceRow(
      'gateway.upstream.request_started',
      'provider_id=1 upstream_url="https://first.example/v1"',
    ),
    traceRow(
      'gateway.upstream.response_received',
      'upstream_status=503 upstream_request_id="previous"',
      'WARN',
    ),
    traceRow(
      'gateway.upstream.request_started',
      'provider_id=2 upstream_url="https://second.example/v1"',
    ),
    traceRow('gateway.upstream.request_failed', 'reason=connection_error provider_id=2', 'ERROR'),
    traceRow('gateway.request.completed', 'status=502 duration_ms=1000', 'ERROR'),
  ];
  const result = analyzeTrace(rows);
  assert.equal(result.diagnosis, 'connection_error');
  assert.equal(result.fields.upstream_status, undefined);
  assert.equal(result.fields.upstream_request_id, undefined);
  assert.equal(result.fields.upstream_url, 'https://second.example/v1');
});
test('legacy upstream bad status stays distinct from the final gateway status', () => {
  const result = analyzeTrace([
    traceRow('gateway.upstream.bad_status', 'status=401', 'WARN'),
    traceRow('gateway.request.completed', 'status=502', 'ERROR'),
  ]);
  assert.equal(result.diagnosis, 'auth');
  assert.equal(result.fields.upstream_status, '401');
  assert.equal(result.fields.status, '502');
});
test('successful final HTTP status and incomplete pages are not reported as confirmed failures', () => {
  const result = analyzeTrace(
    [
      traceRow('gateway.upstream.request_failed', 'reason=timeout', 'ERROR'),
      traceRow('gateway.request.completed', 'status=200'),
    ],
    true,
  );
  assert.equal(result.diagnosis, 'success');
  assert.equal(result.incomplete, true);
  assert.equal(analyzeTrace([]).diagnosis, 'insufficient');
  assert.equal(entryDiagnosis(traceRow('unknown.event', 'some future event')), undefined);
});
test('successful final attempt retains its upstream status and request ID', () => {
  const result = analyzeTrace([
    traceRow('gateway.upstream.request_failed', 'reason=timeout provider_id=1', 'ERROR'),
    traceRow(
      'gateway.upstream.request_started',
      'provider_id=2 upstream_url="https://second.example/v1"',
    ),
    traceRow(
      'gateway.upstream.response_received',
      'upstream_status=200 upstream_request_id="latest"',
    ),
    traceRow('gateway.request.completed', 'status=200 duration_ms=1000'),
  ]);
  assert.equal(result.diagnosis, 'success');
  assert.equal(result.fields.upstream_status, '200');
  assert.equal(result.fields.upstream_request_id, 'latest');
  assert.equal(result.fields.provider_id, '2');
  assert.equal(result.fault, undefined);
});
test('parsed upstream quota failure is more specific than a generic 429', () => {
  const result = analyzeTrace([
    traceRow('gateway.upstream.response_received', 'upstream_status=429', 'WARN'),
    traceRow('gateway.request.completed', 'status=429 error_code=quota_exceeded', 'ERROR'),
  ]);
  assert.equal(result.diagnosis, 'quota');
});

test('HTTP 200 stream failures retain the exact conversion evidence and upstream identity', () => {
  const completed = traceRow(
    'gateway.request.completed',
    'status=200 result_status=502 outcome=error error_code=unsupported_upstream_feature failure_stage=response_conversion error_param="response.content_block.caller.type" error_reason="only direct tool calls can be represented in Chat" stream_frame=5 stream_event="content_block_start" stream_block_index=1 stream_block_type="tool_use"',
    'ERROR',
  );
  const result = analyzeTrace([
    traceRow(
      'gateway.upstream.request_started',
      'provider_id=5 upstream_url="https://api.anthropic.com/v1/messages"',
    ),
    traceRow(
      'gateway.upstream.response_received',
      'upstream_status=200 upstream_request_id="upstream-123"',
    ),
    completed,
  ]);
  assert.equal(result.diagnosis, 'response_conversion');
  assert.equal(result.fault, completed);
  assert.equal(result.fields.status, '200');
  assert.equal(result.fields.result_status, '502');
  assert.equal(result.fields.upstream_status, '200');
  assert.equal(result.fields.upstream_request_id, 'upstream-123');
  assert.equal(result.fields.error_param, 'response.content_block.caller.type');
  assert.equal(result.fields.stream_frame, '5');
  assert.equal(result.fields.stream_block_index, '1');
});

test('conversion failure after retry is not attributed to the earlier upstream rejection', () => {
  const result = analyzeTrace([
    traceRow('gateway.upstream.request_started', 'provider_id=1'),
    traceRow(
      'gateway.upstream.response_received',
      'upstream_status=401 upstream_request_id="old"',
      'WARN',
    ),
    traceRow('gateway.upstream.request_started', 'provider_id=2'),
    traceRow(
      'gateway.upstream.response_received',
      'upstream_status=200 upstream_request_id="current"',
    ),
    traceRow(
      'gateway.request.completed',
      'status=200 outcome=error error_code=unsupported_upstream_feature failure_stage=response_conversion error_param="response.stream"',
      'ERROR',
    ),
  ]);
  assert.equal(result.diagnosis, 'response_conversion');
  assert.equal(result.fields.provider_id, '2');
  assert.equal(result.fields.upstream_request_id, 'current');
  assert.equal(result.fields.upstream_status, '200');
});

test('explicit failed outcomes and legacy stream error codes override HTTP 200', () => {
  for (const [message, expected] of [
    ['status=200 outcome=error', 'unknown'],
    ['status=200 result_status=502', 'unknown'],
    ['status=200 error_code=unsupported_upstream_feature', 'response_conversion'],
    ['status=200 error_code=delivery_interrupted failure_stage=response_write', 'delivery'],
    [
      'status=200 error_code=upstream_error failure_stage=response_read error_reason="upstream response body interrupted: unexpected EOF"',
      'response_read',
    ],
    ['status=200 error_code=upstream_timeout failure_stage=response_read', 'timeout'],
  ]) {
    assert.equal(
      analyzeTrace([traceRow('gateway.request.completed', message, 'ERROR')]).diagnosis,
      expected,
      message,
    );
  }
});

test('usage reporting settings are restricted to super administrators and have matching translations', () => {
  for (const role of ['super_admin', 'org_admin', 'key_viewer']) {
    const paths = getNavigationGroups(role).flatMap(group => group.items.map(item => item.path));
    assert.equal(paths.includes('/admin/usage-reporting'), role === 'super_admin');
  }
  assert.deepEqual(
    Object.keys(usageReportingEnglish).sort(),
    Object.keys(usageReportingChinese).sort(),
  );
  for (const locale of [usageReportingEnglish, usageReportingChinese]) {
    for (const value of Object.values(locale)) assert.ok(value.trim());
  }
});

test('installation telemetry preview rejects business content, credentials and legacy reports', () => {
  const report = {
    schema_version: 2,
    report_id: '00000000-0000-4000-8000-000000000001',
    installation_id: '00000000-0000-4000-8000-000000000002',
    collection_epoch: '00000000-0000-4000-8000-000000000003',
    report_kind: 'installation',
    generated_at: '2026-10-20T00:00:00Z',
    app_version: '1.2.3',
    os: 'linux',
    arch: 'amd64',
  };
  assert.deepEqual(parseInstallationPreview({ report, template: false }), {
    report,
    template: false,
  });
  for (const extra of [
    { prompt: 'private content' },
    { api_key: 'sk-test-full-credential' },
    { usage: { record_count: 1 } },
    { app_version: 'Bearer full-credential' },
    { os: 'private-hostname' },
    { schema_version: 1 },
  ]) {
    assert.throws(() =>
      parseInstallationPreview({ report: { ...report, ...extra }, template: false }),
    );
  }
  assert.equal(
    parseInstallationPreview({
      report: { ...report, installation_id: 'not-created', collection_epoch: 'not-created' },
      template: true,
    }).template,
    true,
  );
  assert.throws(() =>
    parseInstallationPreview({
      report: { ...report, installation_id: 'not-created' },
      template: false,
    }),
  );
  assert.throws(() =>
    parseInstallationPreview({
      report: {
        ...report,
        report_kind: 'daily',
        business_date: '2026-10-19',
        timezone: 'UTC',
      },
      template: false,
    }),
  );
});

test('installation telemetry dates follow UTC midnight and year boundaries', () => {
  for (const [generated_at, business_date] of [
    ['2026-10-19T16:00:00Z', '2026-10-19'],
    ['2026-10-19T23:59:59Z', '2026-10-19'],
    ['2026-10-20T00:00:00Z', '2026-10-20'],
    ['2026-12-31T23:59:59Z', '2026-12-31'],
    ['2027-01-01T00:00:00Z', '2027-01-01'],
  ]) {
    const report = {
      schema_version: 2,
      report_id: '00000000-0000-4000-8000-000000000001',
      installation_id: '00000000-0000-4000-8000-000000000002',
      collection_epoch: '00000000-0000-4000-8000-000000000003',
      report_kind: 'daily',
      generated_at,
      business_date,
      timezone: 'UTC',
      app_version: '1.2.3',
      os: 'linux',
      arch: 'amd64',
    };
    assert.deepEqual(parseInstallationPreview({ report, template: false }).report, report);
    assert.throws(() =>
      parseInstallationPreview({
        report: { ...report, timezone: 'Asia/Shanghai' },
        template: false,
      }),
    );
    const nextDate = new Date(Date.parse(generated_at) + 24 * 60 * 60 * 1000)
      .toISOString()
      .slice(0, 10);
    assert.throws(() =>
      parseInstallationPreview({
        report: { ...report, business_date: nextDate },
        template: false,
      }),
    );
  }
});
