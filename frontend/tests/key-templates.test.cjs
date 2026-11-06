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
    rate_limit_5h: 1000,
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
  assert.equal(settings.rate_limit_5h, 1000);
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
