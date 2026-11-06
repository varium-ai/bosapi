const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const http = require('node:http');
const ts = require('typescript');
const clientPath = path.resolve(__dirname, '../src/api/client.ts');

async function loadClient(baseURL) {
  const axios = (await import('axios')).default;
  const notifications = [];
  const configurationEvents = [];
  const storage = new Map();
  const location = { pathname: '/admin/status', href: '' };
  let refreshRequests = 0;
  const source = ts.transpileModule(fs.readFileSync(clientPath, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
    transformers: {
      before: [
        context => sourceFile => {
          const visit = node =>
            ts.isMetaProperty(node) && node.keywordToken === ts.SyntaxKind.ImportKeyword
              ? context.factory.createIdentifier('testImportMeta')
              : ts.visitEachChild(node, visit, context);
          return ts.visitNode(sourceFile, visit);
        },
      ],
    },
  }).outputText;
  const exports = {};
  vm.runInNewContext(
    source,
    {
      exports,
      testImportMeta: { env: { VITE_API_BASE_URL: baseURL } },
      localStorage: {
        getItem: key => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
        removeItem: key => storage.delete(key),
      },
      window: {
        location,
        dispatchEvent: event => {
          configurationEvents.push(event);
          return true;
        },
      },
      CustomEvent: class {
        constructor(type, { detail }) {
          this.type = type;
          this.detail = detail;
        }
      },
      require: name => {
        if (name === '../features/diagnostics/reportConfigurationError')
          return {
            clearConfigurationError: () =>
              configurationEvents.push({ type: 'bos:configuration-clear' }),
          };
        if (name === 'axios')
          return {
            default: {
              create: options => axios.create({ ...options, proxy: false }),
              isCancel: axios.isCancel,
              post: (...args) => {
                refreshRequests++;
                return axios.post(...args);
              },
            },
          };
        if (name === 'antd')
          return {
            message: {
              error: value => notifications.push(typeof value === 'string' ? value : value.content),
            },
          };
        throw new Error(`Unexpected client dependency: ${name}`);
      },
    },
    { filename: clientPath },
  );
  return {
    apiClient: exports.apiClient,
    logoutKeyPortal: exports.logoutKeyPortal,
    axios,
    notifications,
    configurationEvents,
    storage,
    location,
    refreshRequests: () => refreshRequests,
  };
}

test('background name lookup failures stay local while primary reads and writes remain visible', async () => {
  const client = await loadClient('http://console.invalid');
  client.location.pathname = '/admin/security-events';
  const loaded = { exports: {} };
  const source = ts.transpileModule(
    fs.readFileSync(
      path.resolve(__dirname, '../src/features/common/resourceNameLookup.ts'),
      'utf8',
    ),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } },
  ).outputText;
  vm.runInNewContext(source, { module: loaded, exports: loaded.exports });
  const lookup = loaded.exports.createResourceNameLookup(client.apiClient);
  for (const status of [undefined, 403, 500]) {
    client.apiClient.defaults.adapter = async config => {
      throw new client.axios.AxiosError(
        'failed to load name',
        status ? 'ERR_BAD_RESPONSE' : 'ECONNABORTED',
        config,
        {},
        status ? { status, data: {} } : undefined,
      );
    };
    for (const kind of ['keys', 'organizations']) await assert.rejects(lookup(`/admin/${kind}/42`));
  }
  assert.deepEqual(client.notifications, []);
  assert.deepEqual(client.configurationEvents, []);

  for (const method of ['get', 'post', 'put', 'delete']) {
    await assert.rejects(client.apiClient.request({ url: '/admin/keys', method }));
    const event = client.configurationEvents.at(-1);
    assert.equal(event.type, 'bos:configuration-error');
    assert.equal(event.detail.config.method, method);
    assert.equal(event.detail.configurationFeedbackHandled, true);
  }
  assert.equal(client.configurationEvents.length, 4);
  assert.deepEqual(client.notifications, []);
});

test('API error notifications distinguish cancellation from actual failures', async t => {
  let slowRequest;
  const server = http.createServer(async (request, response) => {
    if (request.url === '/slow') {
      slowRequest?.();
      return;
    }
    if (request.url === '/auth/key-refresh') {
      return setTimeout(() => {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(
          JSON.stringify({
            data: {
              access_token: 'refreshed-key-portal-token',
              key: { api_key_id: 42, name: 'test key', role: 'key_viewer' },
            },
          }),
        );
      }, 20);
    }
    if (request.url === '/auth/key-logout') {
      response.writeHead(200, { 'Content-Type': 'application/json' });
      return response.end(JSON.stringify({ data: { ok: true } }));
    }
    if (request.url === '/auth/mfa/verify') {
      let body = '';
      for await (const chunk of request) body += chunk;
      if (JSON.parse(body).code === '123456') {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        return response.end(JSON.stringify({ data: { access_token: 'verified-mfa-token' } }));
      }
      response.writeHead(401, { 'Content-Type': 'application/json' });
      return response.end(JSON.stringify({ error: { message: 'verification code is invalid' } }));
    }
    if (request.url === '/key-portal/info') {
      if (request.headers.authorization !== 'Bearer refreshed-key-portal-token') {
        response.writeHead(401, { 'Content-Type': 'application/json' });
        return response.end(
          JSON.stringify({ error: { message: 'session is invalid or expired' } }),
        );
      }
      response.writeHead(200, { 'Content-Type': 'application/json' });
      return response.end(JSON.stringify({ data: { api_key_id: 42, name: 'test key' } }));
    }
    if (request.url === '/success') {
      response.writeHead(200, { 'Content-Type': 'application/json' });
      return response.end(JSON.stringify({ data: { status: 'ok' } }));
    }
    const status = request.url === '/canceled-message' ? 500 : Number(request.url.slice(1));
    response.writeHead(status, { 'Content-Type': 'application/json' });
    const message =
      request.url === '/canceled-message'
        ? 'canceled'
        : status === 500
          ? 'request could not be completed'
          : status === 403
            ? 'role is not allowed'
            : 'resource was not found';
    response.end(JSON.stringify({ error: { message }, data: { status: 'degraded' } }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const baseURL = `http://127.0.0.1:${server.address().port}`;
  try {
    await t.test(
      'pre-aborted signals still reject without an error toast or authentication changes',
      async () => {
        const client = await loadClient(baseURL);
        client.storage.set('auth_token', 'existing-test-token');
        client.storage.set('refresh_token', 'existing-test-refresh-token');
        const controller = new AbortController();
        controller.abort();
        await assert.rejects(
          client.apiClient.get('/slow', { signal: controller.signal }),
          error => client.axios.isCancel(error) && error.code === 'ERR_CANCELED',
        );
        assert.deepEqual(client.notifications, []);
        assert.equal(client.storage.get('auth_token'), 'existing-test-token');
        assert.equal(client.storage.get('refresh_token'), 'existing-test-refresh-token');
        assert.equal(client.refreshRequests(), 0);
        assert.equal(client.location.href, '');
      },
    );

    await t.test(
      'in-flight query cancellation does not prevent the next snapshot from loading',
      async () => {
        const client = await loadClient(baseURL);
        const controller = new AbortController();
        const started = new Promise(resolve => {
          slowRequest = resolve;
        });
        const request = client.apiClient.get('/slow', { signal: controller.signal });
        const rejected = assert.rejects(request, error => client.axios.isCancel(error));
        await started;
        controller.abort();
        await rejected;
        assert.deepEqual(client.notifications, []);
        const response = await client.apiClient.get('/success');
        assert.equal(response.data.data.status, 'ok');
      },
    );

    await t.test(
      'CancelToken cancellation is also silent and preserves the rejection',
      async () => {
        const client = await loadClient(baseURL);
        const token = client.axios.CancelToken.source();
        token.cancel('snapshot was superseded');
        await assert.rejects(client.apiClient.get('/slow', { cancelToken: token.token }), error =>
          client.axios.isCancel(error),
        );
        assert.deepEqual(client.notifications, []);
      },
    );

    await t.test('timeouts remain visible request failures', async () => {
      const client = await loadClient(baseURL);
      await assert.rejects(
        client.apiClient.get('/slow', { timeout: 30 }),
        error => error.code === 'ECONNABORTED' && !client.axios.isCancel(error),
      );
      assert.equal(client.notifications.length, 1);
      assert.match(client.notifications[0], /timeout/);
    });

    await t.test(
      'HTTP permission, missing-resource and server errors are still reported',
      async () => {
        const client = await loadClient(baseURL);
        for (const status of [403, 404, 500])
          await assert.rejects(
            client.apiClient.get(`/${status}`),
            error => error.response.status === status,
          );
        assert.deepEqual(client.notifications, [
          'role is not allowed',
          'resource was not found',
          'request could not be completed',
        ]);
      },
    );

    await t.test(
      'a real server error named canceled is not mistaken for a cancellation',
      async () => {
        const client = await loadClient(baseURL);
        await assert.rejects(
          client.apiClient.get('/canceled-message'),
          error => error.response.status === 500 && !client.axios.isCancel(error),
        );
        assert.deepEqual(client.notifications, ['canceled']);
      },
    );

    await t.test('network failures are still reported', async () => {
      const client = await loadClient(baseURL);
      await assert.rejects(
        client.apiClient.get('/success', {
          adapter: async () => {
            throw new client.axios.AxiosError('Network Error', 'ERR_NETWORK');
          },
        }),
        error => error.code === 'ERR_NETWORK',
      );
      assert.deepEqual(client.notifications, ['Network Error']);
    });

    await t.test(
      'the status page can still inspect degraded 503 telemetry without an error toast',
      async () => {
        const client = await loadClient(baseURL);
        const response = await client.apiClient.get('/503', {
          validateStatus: status => (status >= 200 && status < 300) || status === 503,
        });
        assert.equal(response.status, 503);
        assert.equal(response.data.data.status, 'degraded');
        assert.deepEqual(client.notifications, []);
      },
    );

    await t.test(
      'concurrent API key dashboard requests share one refresh and retry without redirecting',
      async () => {
        const client = await loadClient(baseURL);
        client.storage.set('key_portal_token', 'expired-key-portal-token');
        client.storage.set(
          'key_portal_info',
          JSON.stringify({ api_key_id: 42, name: 'old key name' }),
        );
        client.location.pathname = '/key-portal';
        const responses = await Promise.all([
          client.apiClient.get('/key-portal/info'),
          client.apiClient.get('/key-portal/info'),
        ]);
        assert.deepEqual(
          responses.map(response => response.status),
          [200, 200],
        );
        assert.equal(client.storage.get('key_portal_token'), 'refreshed-key-portal-token');
        assert.equal(JSON.parse(client.storage.get('key_portal_info')).name, 'test key');
        assert.equal(JSON.parse(client.storage.get('user_info')).role, 'key_viewer');
        assert.equal(client.refreshRequests(), 1);
        assert.equal(client.location.href, '');
        assert.deepEqual(client.notifications, []);
      },
    );

    await t.test('API key dashboard sign-out clears only the key session state', async () => {
      const client = await loadClient(baseURL);
      client.storage.set('auth_token', 'unrelated-account-token');
      client.storage.set('key_portal_token', 'key-portal-token');
      client.storage.set('key_portal_info', JSON.stringify({ api_key_id: 42 }));
      client.storage.set('user_info', JSON.stringify({ role: 'key_viewer' }));
      await client.logoutKeyPortal();
      assert.equal(client.storage.get('auth_token'), 'unrelated-account-token');
      assert.equal(client.storage.get('key_portal_token'), undefined);
      assert.equal(client.storage.get('key_portal_info'), undefined);
      assert.equal(client.storage.get('user_info'), undefined);
      assert.equal(client.refreshRequests(), 1);
      assert.equal(client.location.href, '/login');
    });

    for (const hasSession of [false, true]) {
      await t.test(
        `MFA failures show the error and allow retry without refreshing or clearing ${hasSession ? 'an existing' : 'a pending'} session`,
        async () => {
          const client = await loadClient(baseURL);
          client.location.pathname = '/login';
          if (hasSession) {
            client.storage.set('auth_token', 'existing-account-token');
            client.storage.set('refresh_token', 'existing-refresh-token');
            client.storage.set('user_info', '{"role":"org_admin"}');
          }
          const before = [...client.storage.entries()];
          await assert.rejects(
            client.apiClient.post('/auth/mfa/verify', {
              mfa_token: 'pending-mfa-token',
              code: '000000',
            }),
            error => error.response.status === 401,
          );
          assert.deepEqual(client.notifications, ['verification code is invalid']);
          assert.equal(client.refreshRequests(), 0);
          assert.equal(client.location.href, '');
          assert.deepEqual([...client.storage.entries()], before);

          const response = await client.apiClient.post('/auth/mfa/verify', {
            mfa_token: 'pending-mfa-token',
            code: '123456',
          });
          assert.equal(response.data.data.access_token, 'verified-mfa-token');
          assert.equal(client.refreshRequests(), 0);
        },
      );
    }
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
});

test('successful configuration writes clear stale diagnostics while reads do not dismiss them', async () => {
  const client = await loadClient('http://console.invalid');
  client.apiClient.defaults.adapter = async config => ({
    data: {},
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  });
  await client.apiClient.get('/admin/providers');
  assert.equal(client.configurationEvents.length, 0);
  for (const method of ['post', 'put', 'patch', 'delete'])
    await client.apiClient.request({ url: '/admin/providers/7', method });
  assert.equal(client.configurationEvents.length, 4);
  assert.ok(client.configurationEvents.every(event => event.type === 'bos:configuration-clear'));
  await client.apiClient.put('/admin/providers/7', {}, { _configurationPath: '/admin/keys' });
  assert.equal(
    client.configurationEvents.length,
    4,
    'a write from a previous page must not dismiss current diagnostics',
  );
});
