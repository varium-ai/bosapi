const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-first-key-test-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/onboarding/firstKey.ts',
      'src/i18n/onboardingMessages.ts',
      '--outDir',
      temporary,
      '--rootDir',
      'src',
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
const {
  positiveID,
  setupOrganizations,
  selectSetupOrganization,
  setupProgress,
  parseSetupPreferences,
  parseSetupEvidence,
  firstKeyPath,
  hasSetupModel,
  collectSetupPages,
} = require(path.join(temporary, 'features/onboarding/firstKey.js'));
const { onboardingEnglish, onboardingChinese } = require(
  path.join(temporary, 'i18n/onboardingMessages.js'),
);
const organizations = [
  { id: 1, name: 'Existing company', status: 'active', has_api_keys: true },
  { id: 2, name: 'New company', status: 'active', has_api_keys: false },
  { id: 3, name: 'Disabled company', status: 'disabled', has_api_keys: false },
];

test('first Key completion is scoped to an active organization, not the administrator or platform', () => {
  const admin = setupOrganizations(organizations, { id: 10, role: 'super_admin' });
  assert.equal(selectSetupOrganization(admin).id, 2);
  assert.equal(selectSetupOrganization(admin, 1).has_api_keys, true);
  assert.equal(selectSetupOrganization(admin, 999).id, 2);
  const org = setupOrganizations(organizations, { id: 11, role: 'org_admin', organization_id: 2 });
  assert.deepEqual(
    org.map(item => item.id),
    [2],
  );
  assert.equal(selectSetupOrganization(org, 1).id, 2);
  assert.deepEqual(
    setupOrganizations(organizations, { id: 12, role: 'key_viewer', organization_id: 2 }),
    [],
  );
  assert.equal(selectSetupOrganization([organizations[0]]), undefined);
});

test('completion requires a valid organization Key, verified channel and current successful call evidence', () => {
  const base = {
    organizationExists: true,
    privateChannel: false,
    channelConfigured: true,
    channelVerified: true,
    organizationReady: true,
    hasUsableKey: true,
  };
  assert.equal(setupProgress(base).current, 'call');
  assert.equal(setupProgress({ ...base, channelVerified: false }).current, 'channel');
  assert.equal(setupProgress({ ...base, hasUsableKey: false }).current, 'key');
  assert.equal(
    setupProgress({
      ...base,
      successfulCallAt: '2026-01-01T00:00:00Z',
      providerUpdatedAt: '2026-02-01T00:00:00Z',
    }).successful,
    false,
  );
  assert.equal(
    setupProgress({
      ...base,
      successfulCallAt: '2026-02-02T00:00:00Z',
      providerUpdatedAt: '2026-02-01T00:00:00Z',
    }).count,
    4,
  );
  assert.equal(
    setupProgress({ ...base, hasUsableKey: false, successfulCallAt: '2026-02-02T00:00:00Z' })
      .successful,
    false,
  );
  assert.equal(setupProgress({ ...base, organizationExists: false }).order[0], 'channel');
  assert.equal(setupProgress({ ...base, organizationExists: false }).order[0], 'channel');
});

test('a channel without an active, allowed model cannot complete setup', () => {
  const channel = { id: 5, name: 'Upstream', status: 'active', schedulable: true };
  const org = { ...organizations[1], has_provider_bindings: true, allowed_models: ['model-a'] };
  assert.equal(hasSetupModel(channel, [], org), false);
  assert.equal(hasSetupModel(channel, [{ model_name: '*' }], org), false);
  assert.equal(hasSetupModel(channel, [{ model_name: 'model-b' }], org), false);
  assert.equal(hasSetupModel(channel, [{ model_name: 'model-a', status: 'disabled' }], org), false);
  assert.equal(
    hasSetupModel({ ...channel, schedulable: false }, [{ model_name: 'model-a' }], org),
    false,
  );
  assert.equal(hasSetupModel(channel, [{ model_name: 'model-a' }], org), true);
  assert.equal(hasSetupModel(channel, [{ model_name: 'models/model-a' }], org), false);
  assert.equal(
    hasSetupModel({ ...channel, api_format: 'gemini' }, [{ model_name: 'models/model-a' }], org),
    true,
  );
  assert.equal(hasSetupModel(channel, [{ model_name: 'model-b' }]), true);
});

test('setup links preserve organization context and use explicit one-time form actions', () => {
  const target = new URL(firstKeyPath('/admin/keys', 2, 'create-key'), 'http://localhost');
  assert.equal(target.pathname, '/admin/keys');
  assert.equal(target.searchParams.get('organization_id'), '2');
  assert.equal(target.searchParams.get('setup_org'), '2');
  assert.equal(target.searchParams.get('setup_action'), 'create-key');
  assert.equal(
    new URL(firstKeyPath('/admin/providers'), 'http://localhost').searchParams.has('setup_action'),
    false,
  );
  for (const invalid of [null, '', '-1', '0', '1.2', 'Infinity', '9007199254740992', '1abc'])
    assert.equal(positiveID(invalid), undefined);
  assert.equal(positiveID('2'), 2);
});

test('guide copy is complete in both console languages', () => {
  assert.deepEqual(Object.keys(onboardingEnglish).sort(), Object.keys(onboardingChinese).sort());
  for (const [key, value] of Object.entries(onboardingChinese)) {
    assert.ok(value.trim(), key);
    assert.deepEqual(
      value.match(/\{\w+\}/g) || [],
      onboardingEnglish[key].match(/\{\w+\}/g) || [],
      key,
    );
  }
});

test('setup reads every page and never treats an unavailable or partial status as zero resources', async () => {
  const calls = [];
  const items = await collectSetupPages(async page => {
    calls.push(page);
    return { items: page === 1 ? Array.from({ length: 100 }, (_, i) => i + 1) : [101], total: 101 };
  });
  assert.deepEqual(calls, [1, 2]);
  assert.equal(items.length, 101);
  await assert.rejects(
    collectSetupPages(async () => {
      throw new Error('denied');
    }),
    /denied/,
  );
  await assert.rejects(
    collectSetupPages(async () => ({ items: [], total: 1 })),
    /Incomplete/,
  );
  await assert.rejects(
    collectSetupPages(async () => ({ items: [], total: undefined })),
    /Invalid/,
  );
  await assert.rejects(
    collectSetupPages(async () => ({ items: [], total: NaN })),
    /Invalid/,
  );
});

test('invalid browser preferences never affect progress or crash the console', () => {
  for (const raw of [null, 'null', '[]', 'false', 'invalid-json'])
    assert.deepEqual(parseSetupPreferences(raw), {});
  assert.deepEqual(
    parseSetupPreferences('{"7":true,"8":false,"instance":true,"9":"yes","-1":true}'),
    { 7: true, 8: false, instance: true },
  );
});

test('partial or unavailable server evidence never looks like an empty organization', () => {
  const valid = {
    has_usable_key: true,
    successful_call_at: null,
    provider_id: 7,
    provider_updated_at: '2026-01-01T00:00:00Z',
    channel_verified: true,
    can_test: false,
  };
  assert.equal(parseSetupEvidence(valid).provider_id, 7);
  for (const value of [
    null,
    {},
    { ...valid, provider_id: -1 },
    { ...valid, has_usable_key: undefined },
    { ...valid, successful_call_at: 'invalid date' },
    { ...valid, channel_verified: 'true' },
  ])
    assert.throws(() => parseSetupEvidence(value), /Incomplete/);
});
