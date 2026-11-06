const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const { performance } = require('node:perf_hooks');
const { execFileSync } = require('node:child_process');
const cache = new Map();
function load(file) {
  file = path.resolve(file);
  if (cache.has(file)) return cache.get(file);
  const result = { exports: {} };
  cache.set(file, result.exports);
  const code = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  new Function('exports', 'require', 'module', code)(
    result.exports,
    name =>
      name.startsWith('.') ? load(path.resolve(path.dirname(file), name + '.ts')) : require(name),
    result,
  );
  return result.exports;
}
const source = name => path.resolve(__dirname, '../src', name);
const catalog = load(source('features/diagnostics/catalog.ts'));
const { buildSearchIndex, searchEntries } = load(source('features/search/searchIndex.ts'));
const { configurationErrorDoc } = load(source('features/diagnostics/documentation.ts'));
const docs = require('../src/features/search/docsMetadata.json');
const index = (role = 'super_admin', language = 'zh-CN', org = 7) =>
  buildSearchIndex(role, language, key => key, docs, org);

test('Chinese, English, aliases and stable errors resolve to exact destinations', () => {
  const entries = index();
  for (const query of ['上游', '渠道', '供应商', 'upstream', 'provider'])
    assert.ok(
      searchEntries(entries, query).some(item => item.href === '/admin/providers'),
      query,
    );
  for (const query of ['创建 Key', '密钥', '令牌', 'create key'])
    assert.ok(
      searchEntries(entries, query).some(item => item.id === 'create-key'),
      query,
    );
  for (const query of ['组织', '分配模型', 'organization'])
    assert.ok(
      searchEntries(entries, query).some(item => item.href === '/admin/organizations'),
      query,
    );
  for (const query of ['超时', '认证失败', 'timeout'])
    assert.ok(
      searchEntries(entries, query).some(item =>
        item.href.startsWith('/docs/configuration-errors#'),
      ),
      query,
    );
  for (const code of [
    ...catalog.configurationIssues.map(item => item.code),
    ...Object.keys(catalog.issueAliases),
  ]) {
    assert.equal(searchEntries(entries, code)[0].href, catalog.issueHref(code), code);
    const id = catalog.issueHref(code).split('#')[1];
    for (const lang of ['zh-CN', 'en-US'])
      assert.ok(
        configurationErrorDoc(lang).sections.some(section => section.id === id),
        code,
      );
  }
  assert.deepEqual(searchEntries(entries, 'does-not-exist-12345'), []);
  assert.ok(searchEntries(entries, '').every(item => item.kind !== 'help'));
});

test('role, tenant and language indexes are rebuilt without privileged actions', () => {
  assert.deepEqual(
    index(undefined)
      .filter(entry => entry.kind === 'page')
      .map(entry => entry.href),
    index('super_admin')
      .filter(entry => entry.kind === 'page')
      .map(entry => entry.href),
  );
  assert.deepEqual(
    buildSearchIndex(undefined, 'en-US', key => key, docs),
    [],
  );
  for (const role of ['org_admin', 'key_viewer']) {
    const entries = index(role);
    assert.ok(!entries.some(item => item.id === 'create-organization'));
    assert.ok(
      !entries.some(item => ['/admin/system-updates', '/admin/security-rules'].includes(item.href)),
    );
  }
  assert.ok(
    index('key_viewer')
      .filter(item => item.kind === 'page')
      .every(item => item.href.startsWith('/key-portal')),
  );
  assert.ok(!index('key_viewer').some(item => item.kind === 'action'));
  for (const org of [7, 8])
    assert.equal(
      new URL(
        index('org_admin', 'zh-CN', org).find(item => item.id === 'create-key').href,
        'https://console.invalid',
      ).searchParams.get('setup_org'),
      String(org),
    );
  assert.notEqual(
    index('org_admin', 'zh-CN').find(item => item.id === 'create-key').name,
    index('org_admin', 'en-US').find(item => item.id === 'create-key').name,
  );
});

test('error classification uses codes and transport evidence, never error message substrings', () => {
  for (const item of catalog.configurationIssues)
    assert.equal(
      catalog.classifyConfigurationError({ response: { data: { error: { code: item.code } } } }),
      item.code,
    );
  assert.equal(
    catalog.classifyConfigurationError({
      response: {
        status: 500,
        data: { error: { code: 'not_registered', message: '401 balance timeout' } },
      },
    }),
    'unknown_error',
  );
  assert.equal(
    catalog.classifyConfigurationError({ code: 'ECONNABORTED', config: { method: 'post' } }),
    'submission_uncertain',
  );
  assert.equal(
    catalog.classifyConfigurationError({ code: 'ECONNABORTED', config: { method: 'get' } }),
    'request_timeout',
  );
  assert.equal(
    catalog.classifyConfigurationError({ code: 'ERR_NETWORK', config: { method: 'get' } }),
    'network_error',
  );
  assert.equal(catalog.classifyConfigurationError({ response: { status: 429 } }), 'rate_limited');
  const preview = catalog.diagnosticPreview(
    {
      config: {
        data: 'secret-key',
        headers: { Authorization: 'secret-token' },
        url: '/secret?key=secret',
      },
      message: 'secret-stack',
      response: {
        status: 502,
        data: { request_id: 'req_valid-123', error: { message: 'secret-db' } },
      },
    },
    'provider_auth_failed',
  );
  assert.ok(!preview.includes('secret'));
  assert.equal(JSON.parse(preview).request_id, 'req_valid-123');
  assert.ok(!('request_id' in JSON.parse(catalog.diagnosticPreview({}, 'network_error'))));
});

test('OAuth and policy failures preserve their own evidence instead of inferring HTTP 401 or account permissions', () => {
  for (const code of [
    'invalid_oauth_session',
    'oauth_exchange_failed',
    'account_identity_missing',
    'security_blocked',
    'unauthorized_tool',
  ]) {
    assert.equal(catalog.issueByCode(code).code, code);
    assert.equal(catalog.issueHref(code), `/docs/configuration-errors#${code}`);
  }
});

test('configuration feedback identifies the failed operation without echoing a sensitive URL', () => {
  assert.equal(
    catalog.configurationOperation(
      { config: { url: '/admin/providers/7/test?key=secret' } },
      '/',
      'zh-CN',
    ),
    '验证渠道连接',
  );
  assert.equal(
    catalog.configurationOperation(
      { config: { url: '/admin/keys', method: 'post' } },
      '/',
      'en-US',
    ),
    'Create API Key',
  );
  assert.equal(
    catalog.configurationOperation({}, '/admin/organizations', 'zh-CN'),
    '配置组织及授权',
  );
  assert.equal(
    catalog.configurationOperation({ operation: 'verify-call' }, '/', 'zh-CN'),
    '验证首次调用',
  );
  assert.ok(
    !catalog
      .configurationOperation({ config: { url: '/secret?key=secret' } }, '/', 'en-US')
      .includes('secret'),
  );
});

test('page reads identify the data being loaded even outside its configuration page', () => {
  for (const [url, en, zh] of [
    ['/admin/keys', 'Load API Key information', '加载 API Key 信息'],
    ['/admin/keys/42?token=secret', 'Load API Key information', '加载 API Key 信息'],
    ['/admin/organizations/7', 'Load organization access information', '加载组织及授权信息'],
    ['/admin/providers', 'Load channel information', '加载渠道信息'],
    ['/admin/providers/7/models', 'Load channel models', '加载渠道模型'],
    ['/admin/providers/oauth/status', 'Load channel authorization status', '加载渠道授权状态'],
    ['/unrecognized?token=secret', 'Load data', '加载数据'],
  ]) {
    for (const method of ['get', 'GET', 'head']) {
      const error = { config: { url, method } };
      assert.equal(catalog.configurationOperation(error, '/admin/security-events', 'en-US'), en);
      assert.equal(catalog.configurationOperation(error, '/admin/security-events', 'zh-CN'), zh);
      assert.equal(
        catalog.classifyConfigurationError({ ...error, code: 'ECONNABORTED' }),
        'request_timeout',
      );
    }
  }
  for (const url of ['/admin/keys/import', '/admin/keys/42/reveal', '/admin/keys/42/reset'])
    assert.notEqual(
      catalog.configurationOperation({ config: { url, method: 'post' } }, '/', 'en-US'),
      'Create API Key',
    );
  for (const method of ['post', 'put', 'patch', 'delete'])
    assert.equal(
      catalog.classifyConfigurationError({ code: 'ECONNABORTED', config: { method } }),
      'submission_uncertain',
    );
});

test('read timeout guidance states the evidence and suggests reloading instead of checking a write', () => {
  const timeout = catalog.issueByCode('request_timeout');
  assert.match(catalog.issueText(timeout.reason, 'en-US'), /does not establish the cause/);
  assert.match(catalog.issueText(timeout.reason, 'zh-CN'), /无法确定具体原因/);
  assert.match(catalog.issueText(timeout.checks, 'en-US')[0], /Retry loading/);
  assert.match(catalog.issueText(timeout.checks, 'zh-CN')[0], /重试加载/);
});

test('each bilingual issue has ordered checks, permissions, verification and feedback', () => {
  const en = configurationErrorDoc('en-US');
  const zh = configurationErrorDoc('zh-CN');
  assert.deepEqual(
    en.sections.map(item => item.id),
    zh.sections.map(item => item.id),
  );
  assert.equal(new Set(en.sections.map(item => item.id)).size, en.sections.length);
  for (const section of [...en.sections, ...zh.sections]) {
    assert.ok(section.blocks.length >= 6);
    assert.ok(
      section.blocks.some(
        block => block.type === 'list' && block.ordered && block.items.length >= 2,
      ),
    );
  }
});

test('search metadata remains synchronized with shipped documentation', () => {
  execFileSync(process.execPath, ['scripts/generate-search-metadata.cjs', '--check'], {
    cwd: path.resolve(__dirname, '..'),
    stdio: 'pipe',
  });
});

test('metadata-only search stays within a 16ms local computation budget', () => {
  const entries = index();
  const times = [];
  for (let i = 0; i < 1000; i++) {
    const start = performance.now();
    searchEntries(entries, ['渠道', 'provider_auth_failed', 'create key', '超时'][i % 4]);
    times.push(performance.now() - start);
  }
  times.sort((a, b) => a - b);
  const p95 = times[Math.floor(times.length * 0.95)];
  console.log(
    `Search: ${entries.length} metadata entries, local p95=${p95.toFixed(3)}ms, max=${times.at(-1).toFixed(3)}ms`,
  );
  assert.ok(p95 < 16, `p95=${p95}ms`);
});
