const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-key-templates-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/keys/templateSettings.ts',
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
const { getKeyTemplateSettings, emptyKeyTemplateSettings } = require(
  path.join(temporary, 'templateSettings.js'),
);

test('template settings preserve organization, expiration, zero limits and IP policy without credential material', () => {
  const settings = getKeyTemplateSettings({
    organization_id: 3,
    user_id: 19,
    expires_at: '2030-01-01T00:00:00Z',
    quota_tokens: 0,
    daily_quota_amount: 0,
    ip_allowlist: ['192.0.2.0/24'],
    ip_blocklist: ['::1'],
    name: 'Application credential',
    api_key: 'must-not-be-stored',
    key_hash: 'private',
    created_by: 20,
  });
  assert.equal(settings.organization_id, 3);
  assert.equal('user_id' in settings, false);
  assert.equal(settings.quota_tokens, 0);
  assert.equal(settings.daily_quota_amount, 0);
  assert.equal(settings.expires_at, '2030-01-01T00:00:00Z');
  assert.equal('rate_limit_5h' in settings, false);
  for (const field of ['name', 'api_key', 'key_hash', 'created_by'])
    assert.equal(field in settings, false);
});

test('switching templates clears limits omitted by the new template and isolates array edits', () => {
  const original = {
    organization_id: 3,
    user_id: 19,
    ip_allowlist: ['192.0.2.1'],
    ip_blocklist: ['::1'],
    quota_tokens: 1000,
  };
  const filled = getKeyTemplateSettings(original);
  filled.ip_allowlist.push('192.0.2.2');
  filled.ip_blocklist.length = 0;
  assert.deepEqual(original.ip_allowlist, ['192.0.2.1']);
  assert.deepEqual(original.ip_blocklist, ['::1']);
  const values = { ...filled, ...getKeyTemplateSettings({ organization_id: 4, user_id: 20 }) };
  assert.equal(values.quota_tokens, null);
  assert.deepEqual(values.ip_allowlist, []);
  assert.deepEqual(values.ip_blocklist, []);
  assert.equal('user_id' in values, false);
  assert.deepEqual(
    getKeyTemplateSettings({
      organization_id: 3,
      user_id: 19,
      ip_allowlist: null,
      ip_blocklist: null,
    }),
    emptyKeyTemplateSettings(3),
  );
});

test('custom quota cycle survives import and template saves and clears when switching templates', () => {
  const start = '2026-11-26T00:00:00Z';
  const settings = getKeyTemplateSettings({ organization_id: 3, quota_reset_start_at: start });
  assert.equal(settings.quota_reset_start_at, start);
  const payload = { keys: [{ name: 'Imported', api_key: 'import-fixture' }], ...settings };
  assert.equal(JSON.parse(JSON.stringify(payload)).quota_reset_start_at, start);
  const switched = { ...settings, ...getKeyTemplateSettings({ organization_id: 3 }) };
  assert.equal(switched.quota_reset_start_at, null);
  assert.equal(getKeyTemplateSettings({ quota_reset_start_at: null }).quota_reset_start_at, null);
});

test('batch import hides unsupported rolling and concurrency fields while keeping periodic quotas', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/keys/KeysPage.tsx'), 'utf8');
  const fields = source.match(/<KeyConfigurationFields\b[\s\S]*?\/>/g);
  const imported = fields.find(field => field.includes('selectedImportTemplateID'));
  assert.ok(imported);
  assert.match(imported, /showRollingLimits=\{false\}/);
  assert.match(imported, /showTokenLimits=\{true\}/);
});
