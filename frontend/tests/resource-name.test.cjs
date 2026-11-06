const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-resource-name-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/common/resourceNameLookup.ts',
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
const { createResourceNameLookup } = require(path.join(temporary, 'resourceNameLookup.js'));

test('missing historical names do not enter the shared API error interceptor', async () => {
  const axios = (await import('axios')).default;
  const client = axios.create();
  const notifications = [];
  client.interceptors.response.use(undefined, error => {
    notifications.push(error.response?.status);
    return Promise.reject(error);
  });
  const responses = [
    { status: 200, data: { data: { name: 'Existing key' } } },
    { status: 404, data: { error: { message: 'resource was not found' } } },
    { status: 403, data: { error: { message: 'role is not allowed' } } },
    { status: 500, data: { error: { message: 'request could not be completed' } } },
  ];
  const server = require('node:http').createServer((request, response) => {
    const result = responses[Number(request.url.slice(1))];
    response.writeHead(result.status, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify(result.data));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const lookup = createResourceNameLookup(client);
  try {
    assert.equal(await lookup(`${base}/0`), 'Existing key');
    assert.equal(await lookup(`${base}/1`), '');
    assert.deepEqual(notifications, []);
    await assert.rejects(lookup(`${base}/2`), error => error.response?.status === 403);
    await assert.rejects(lookup(`${base}/3`), error => error.response?.status === 500);
    assert.deepEqual(notifications, [403, 500]);
    // Other requests keep Axios' default status handling.
    await assert.rejects(client.get(`${base}/1`), error => error.response?.status === 404);
    assert.deepEqual(notifications, [403, 500, 404]);
  } finally {
    await new Promise(resolve => server.close(resolve));
  }
});

test('only successful responses and resource absence are accepted', async () => {
  const lookup = createResourceNameLookup({
    get: async (url, config) => {
      assert.equal(config.silent, true);
      for (const status of [200, 201, 204, 299, 404])
        assert.equal(config.validateStatus(status), true);
      for (const status of [199, 300, 400, 401, 403, 429, 500, 502, 503])
        assert.equal(config.validateStatus(status), false);
      return { status: 404, data: { data: { name: 'Do not use a name from an error response' } } };
    },
  });
  assert.equal(await lookup('/admin/keys/1'), '');
});

test('queued name lookups stay bounded and release slots after errors', async () => {
  const pending = [];
  let active = 0;
  let maximum = 0;
  const lookup = createResourceNameLookup({
    get: async () => {
      active++;
      maximum = Math.max(maximum, active);
      try {
        return await new Promise((resolve, reject) => pending.push({ resolve, reject }));
      } finally {
        active--;
      }
    },
  });
  const requests = Array.from({ length: 12 }, (_, id) => lookup(`/admin/keys/${id + 1}`));
  const completed = Promise.allSettled(requests);
  assert.equal(pending.length, 4);
  pending.shift().reject(new Error('server failure'));
  for (let finished = 1; finished < requests.length; finished++) {
    await new Promise(resolve => setImmediate(resolve));
    assert.ok(pending.length > 0);
    pending.shift().resolve({ status: 200, data: { data: { name: 'Existing key' } } });
  }
  const results = await completed;
  assert.equal(maximum, 4);
  assert.equal(active, 0);
  assert.equal(results.filter(result => result.status === 'rejected').length, 1);
  const next = lookup('/admin/keys/99');
  pending.shift().resolve({ status: 200, data: { data: { name: 'Next key' } } });
  assert.equal(await next, 'Next key');
});
