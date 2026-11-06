const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-key-models-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/keys/keyModelOptions.ts',
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
const { keyModelChannels, loadKeyChannelModels, buildKeyModelOptions } = require(
  path.join(temporary, 'keyModelOptions.js'),
);

const channels = [
  { id: 10, name: 'Public' },
  { id: 11, name: 'Private' },
];
const catalogs = [
  {
    channel: channels[0],
    models: [
      { model_name: 'shared', status: 'active' },
      { model_name: 'public-model', status: 'active' },
      { model_name: 'disabled', status: 'disabled' },
    ],
  },
  {
    channel: channels[1],
    models: [
      { model_name: 'shared', status: 'active' },
      { model_name: 'private-model', status: 'active' },
      { model_name: 'shared', status: 'active' },
    ],
  },
];

test('empty channel restrictions inherit all organization channels and merge shared active models', () => {
  assert.deepEqual(keyModelChannels(channels), channels);
  assert.deepEqual(buildKeyModelOptions(catalogs), [
    { value: 'private-model', label: 'private-model — Private' },
    { value: 'public-model', label: 'public-model — Public' },
    { value: 'shared', label: 'shared — Public, Private' },
  ]);
});

test('channel selections narrow model suggestions and never include another organization channel', () => {
  const selected = keyModelChannels(channels, [11, 999]);
  assert.deepEqual(selected, [channels[1]]);
  assert.deepEqual(
    buildKeyModelOptions(
      catalogs.filter(item => selected.some(channel => channel.id === item.channel.id)),
    ).map(item => item.value),
    ['private-model', 'shared'],
  );
  assert.deepEqual(keyModelChannels(channels, [999]), []);
});

test('model discovery loads all pages rather than dropping models beyond the first 100', async () => {
  const calls = [];
  const items = Array.from({ length: 107 }, (_, index) => ({
    model_name: `model-${index}`,
    status: 'active',
  }));
  const result = await loadKeyChannelModels(async page => {
    calls.push(page);
    return { items: items.slice((page - 1) * 100, page * 100), total: items.length };
  });
  assert.deepEqual(calls, [1, 2]);
  assert.deepEqual(result, items);
});

test('catalog failures surface as errors instead of an apparently empty or partial model list', async () => {
  await assert.rejects(
    loadKeyChannelModels(async page => {
      if (page === 2) throw new Error('catalog unavailable');
      return { items: [{ model_name: 'first' }], total: 2 };
    }),
    /catalog unavailable/,
  );
  assert.deepEqual(await loadKeyChannelModels(async () => ({ items: [], total: 0 })), []);
});

test('paused or inactive channels cannot make readiness appear callable', () => {
  assert.deepEqual(
    keyModelChannels([
      ...channels,
      { id: 12, name: 'Paused', schedulable: false },
      { id: 13, name: 'Disabled', status: 'inactive' },
    ]),
    channels,
  );
});
