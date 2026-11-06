const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-key-integration-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/keys/ccSwitch.ts',
      'src/features/keys/usagePresentation.ts',
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
const { buildCCSwitchLink } = require(path.join(temporary, 'features/keys/ccSwitch.js'));
const { buildClientImportLink } = require(path.join(temporary, 'features/keys/clientImport.js'));
const { primaryQuota, usageNumber } = require(
  path.join(temporary, 'features/keys/usagePresentation.js'),
);

test('usage distinguishes unavailable data from measured zero', () => {
  for (const value of [undefined, null, '', '1.5', NaN, Infinity, -1])
    assert.equal(usageNumber(value), null);
  assert.equal(usageNumber(0), 0);
  assert.equal(usageNumber(1.25), 1.25);
});

test('quota summary shows the most constrained configured quota, including token-only keys', () => {
  assert.equal(primaryQuota({ quota_used_amount: 8 }), null);
  assert.deepEqual(
    primaryQuota({
      quota_amount: 100,
      quota_used_amount: 30,
      daily_quota_amount: 10,
      daily_used_amount: 9,
    }),
    {
      period: 'daily',
      unit: 'amount',
      used: 9,
      limit: 10,
      ratio: 0.9,
    },
  );
  assert.deepEqual(primaryQuota({ quota_tokens: 1000, quota_used_tokens: 600 }), {
    period: 'total',
    unit: 'tokens',
    used: 600,
    limit: 1000,
    ratio: 0.6,
  });
  assert.equal(primaryQuota({ quota_amount: 0, quota_used_amount: 0 }).ratio, 1);
  assert.equal(primaryQuota({ quota_amount: 10, quota_used_amount: 12 }).ratio, 1.2);
  assert.equal(primaryQuota({ quota_amount: 10 }).ratio, null);
  assert.equal(
    primaryQuota({
      quota_amount: 100,
      quota_used_amount: 1,
      quota_tokens: 100,
      quota_used_tokens: 90,
    }).unit,
    'amount',
  );
});

test('usage cell uses rolling usage instead of relabeling calendar-month or total values', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/keys/KeyUsageCell.tsx'), 'utf8');
  assert.match(source, /money\(record.usage_30d_amount\)/);
  assert.match(source, /usage_today_amount/);
  assert.doesNotMatch(source, /useQuery|apiClient|monthly_used_amount/);
  assert.match(source, /quota &&/);
  assert.match(source, /quota.ratio !== null/);
  assert.match(source, /Math.min\(100/);
  assert.match(source, /<Popover[\s\S]*?<Button[\s\S]*?InfoCircleOutlined[\s\S]*?<\/Popover>/);
  assert.doesNotMatch(source, /tabIndex=|cursor: 'help'|<Table/);
});

test('CC Switch imports only the current gateway and uses client-specific paths', () => {
  for (const app of ['claude', 'codex']) {
    const url = new URL(
      buildCCSwitchLink({
        app,
        name: 'Production',
        endpoint: 'https://gateway.example/proxy/v1/',
        apiKey: 'fixture-secret',
      }),
    );
    assert.equal(url.protocol, 'ccswitch:');
    assert.equal(url.host, 'v1');
    assert.equal(url.pathname, '/import');
    assert.equal(url.searchParams.get('resource'), 'provider');
    assert.equal(url.searchParams.get('app'), app);
    assert.equal(url.searchParams.get('name'), 'BOS API - Production');
    assert.equal(
      url.searchParams.get('endpoint'),
      app === 'claude' ? 'https://gateway.example/proxy' : 'https://gateway.example/proxy/v1',
    );
    assert.equal(url.searchParams.get('homepage'), 'https://gateway.example');
    assert.equal(url.searchParams.get('enabled'), 'true');
    assert.equal(url.searchParams.has('configUrl'), false);
    assert.equal(url.searchParams.has('usageScript'), false);
    assert.equal(url.searchParams.has('model'), false);
  }
});

test('Unicode names and literal credentials are correctly URL encoded without a model override', () => {
  const secret = 'fixture+secret&name=other/#?%\u6d4b\u8bd5';
  const url = new URL(
    buildCCSwitchLink({
      app: 'codex',
      name: '\u6d4b\u8bd5 key & client',
      endpoint: 'https://gateway.example',
      apiKey: secret,
      model: ' vendor/model-v1 ',
    }),
  );
  assert.equal(url.searchParams.get('apiKey'), secret);
  assert.equal(url.searchParams.get('name'), 'BOS API - \u6d4b\u8bd5 key & client');
  assert.equal(url.searchParams.has('model'), false);
  assert.equal(url.hash, '');
});

test('invalid credentials, unknown clients and unsafe gateway URLs are rejected', () => {
  const input = {
    app: 'claude',
    name: 'fixture',
    endpoint: 'https://gateway.example',
    apiKey: 'fixture-secret',
  };
  for (const apiKey of ['', '  ', 'fixture\nsecret', 'fixture\rsecret'])
    assert.throws(() => buildCCSwitchLink({ ...input, apiKey }));
  assert.throws(() => buildCCSwitchLink({ ...input, app: 'gemini' }));
  for (const endpoint of [
    '',
    'javascript:alert(1)',
    'ftp://gateway.example',
    'https://user:pass@gateway.example',
    'https://gateway.example?key=secret',
    'https://gateway.example#fragment',
  ])
    assert.throws(() => buildCCSwitchLink({ ...input, endpoint }));
});

test('configured HTTPS and HTTP addresses generate the correct client endpoints', () => {
  for (const endpoint of ['https://gateway.example', 'http://gateway.example']) {
    for (const app of ['claude', 'codex']) {
      const link = new URL(
        buildCCSwitchLink({
          app,
          name: 'fixture',
          endpoint: `${endpoint}/v1/`,
          apiKey: 'fixture-secret',
        }),
      );
      assert.equal(
        link.searchParams.get('endpoint'),
        app === 'codex' ? `${endpoint}/v1` : endpoint,
      );
    }
  }
});

test('Vary AI imports use its registered protocol and retain gateway paths and literal credentials', () => {
  const secret = 'fixture+secret&name=other/#?%\u6d4b\u8bd5';
  for (const endpoint of ['https://gateway.example/proxy', 'http://gateway.example']) {
    for (const app of ['claude', 'codex']) {
      const link = new URL(
        buildClientImportLink('vary-ai', {
          app,
          name: '\u6d4b\u8bd5 key & client',
          endpoint: `${endpoint}/v1/`,
          apiKey: secret,
        }),
      );
      assert.equal(link.protocol, 'varyai:');
      assert.equal(link.host, 'v1');
      assert.equal(link.pathname, '/import');
      assert.deepEqual(Object.fromEntries(link.searchParams), {
        resource: 'provider',
        app,
        name: 'BOS API - \u6d4b\u8bd5 key & client',
        homepage: new URL(endpoint).origin,
        endpoint: app === 'codex' ? `${endpoint}/v1` : endpoint,
        apiKey: secret,
        enabled: 'true',
      });
      assert.equal(link.hash, '');
    }
  }
});

test('client imports reject unknown targets, invalid keys, clients and unsafe gateway URLs', () => {
  const input = {
    app: 'claude',
    name: 'fixture',
    endpoint: 'https://gateway.example',
    apiKey: 'fixture-secret',
  };
  for (const target of ['vary-ai', 'cc-switch']) {
    for (const apiKey of ['', '  ', 'fixture\nsecret', 'fixture\rsecret'])
      assert.throws(() => buildClientImportLink(target, { ...input, apiKey }));
    assert.throws(() => buildClientImportLink(target, { ...input, app: 'gemini' }));
    for (const endpoint of [
      'javascript:alert(1)',
      'ftp://gateway.example',
      'https://user:pass@gateway.example',
      'https://gateway.example?key=secret',
      'https://gateway.example#fragment',
    ])
      assert.throws(() => buildClientImportLink(target, { ...input, endpoint }));
  }
  for (const target of ['unknown', 'javascript', 'toString', '__proto__'])
    assert.throws(() => buildClientImportLink(target, input));
});

test('viewing and importing keys retain OTP verification and never prefetch secrets', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/keys/KeySecretActions.tsx'), 'utf8');
  assert.match(source, /\/reveal`, \{ code \}/);
  assert.equal((source.match(/readSecret\(keyInfo.id, code\)/g) || []).length, 2);
  assert.equal((source.match(/\^\\d\{6\}\$/g) || []).length, 2);
  assert.doesNotMatch(
    source,
    /clipboard|localStorage|sessionStorage|useQuery|useMutation|useEffect/,
  );
  assert.doesNotMatch(source, /cc_switch_model|setModel/);
  assert.match(source, /setSecret\(null\)/);
  assert.match(source, /setCode\(''\)/);
  assert.match(source, /buildClientImportLink\(target,/);
  assert.match(source, /setImporting\(\{ target: 'vary-ai', open: true \}\)/);
  assert.match(source, /setImporting\(\{ target: 'cc-switch', open: true \}\)/);
  const page = fs.readFileSync(path.join(root, 'src/features/keys/KeysPage.tsx'), 'utf8');
  assert.match(page, /onClick=\{\(\) => setImportTarget\(record\)\}/);
  assert.match(page, /onClick=\{\(\) => setVaryImportTarget\(record\)\}/);
  assert.match(page, /<KeyClientImportModal\s+target="vary-ai"/);
  assert.doesNotMatch(page, /<Dropdown/);
  assert.match(page, /className="console-table-actions"/);
  assert.doesNotMatch(page, /clipboard/i);
  const connection = fs.readFileSync(
    path.join(root, 'src/features/common/ConnectionSettings.tsx'),
    'utf8',
  );
  assert.match(connection, /<Collapse\s+items=/);
  assert.doesNotMatch(connection, /OpenAI-compatible SDK/);
});

test('key UI translations stay aligned, concise and describe retained MFA', () => {
  const english = require(path.join(temporary, 'i18n/locales/en-US.js')).enUS.keys;
  const chinese = require(path.join(temporary, 'i18n/locales/zh-CN.js')).zhCN.keys;
  assert.deepEqual(Object.keys(english).sort(), Object.keys(chinese).sort());
  assert.equal(chinese.col_key, '密钥');
  assert.doesNotMatch(JSON.stringify(chinese), /秘钥/);
  assert.ok(chinese.modal_reveal_title.includes('\u53cc\u91cd\u8ba4\u8bc1'));
  for (const key of [
    'cc_switch_import',
    'cc_switch_client',
    'cc_switch_note',
    'cc_switch_open',
    'vary_ai_import',
    'vary_ai_note',
    'vary_ai_opened',
    'vary_ai_failed',
    'code_example',
  ]) {
    assert.ok(english[key].trim());
    assert.ok(chinese[key].trim());
  }
});
