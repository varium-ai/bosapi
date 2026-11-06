const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

function loadGateway(fetch) {
  const endpointExports = {};
  const transpile = filename =>
    ts.transpileModule(
      fs.readFileSync(path.resolve(__dirname, '../src/config', filename), 'utf8'),
      {
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
      },
    ).outputText;
  vm.runInNewContext(transpile('gatewayEndpoint.ts'), { exports: endpointExports, URL });
  const exports = {};
  vm.runInNewContext(transpile('gateway.ts'), {
    exports,
    fetch,
    testImportMeta: {
      env: { VITE_API_BASE_URL: '/api/v1', VITE_GATEWAY_URL: 'https://old-build.example' },
    },
    window: { __APP_CONFIG__: { gatewayEndpoint: 'https://old-runtime.example' } },
    require: name => {
      if (name === './gatewayEndpoint') return endpointExports;
      if (name === '@tanstack/react-query') return { queryOptions: options => options };
      throw new Error(`Unexpected dependency: ${name}`);
    },
  });
  return exports;
}

const snapshot = value => JSON.parse(JSON.stringify(value));

test('public API settings support an unconfigured instance and use the saved address after configuration', async () => {
  let endpoint = '';
  const requests = [];
  const gateway = loadGateway(async (url, options) => {
    requests.push({ url, options });
    return { ok: true, json: async () => ({ data: { gateway_endpoint: endpoint } }) };
  });
  assert.deepEqual(snapshot(gateway.getGatewayConfig()), { endpoint: '', baseUrl: '' });
  assert.deepEqual(snapshot(await gateway.fetchPlatformGatewayConfig()), {
    endpoint: '',
    baseUrl: '',
  });
  endpoint = 'https://configured.example/proxy/v1/';
  const controller = new AbortController();
  const config = await gateway.gatewayConfigQueryOptions().queryFn({ signal: controller.signal });
  assert.deepEqual(snapshot(config), {
    endpoint: 'https://configured.example/proxy',
    baseUrl: 'https://configured.example/proxy/v1',
  });
  assert.equal(requests[1].url, '/api/v1/platform/config');
  assert.equal(requests[1].options.signal, controller.signal);
  assert.equal(requests[1].options.cache, 'no-store');
  assert.equal(requests[1].options.credentials, 'omit');
});

test('configuration outages reject the fetch instead of supplying a different API address', async () => {
  for (const fetch of [
    async () => ({ ok: false }),
    async () => {
      throw new Error('network unavailable');
    },
    async () => ({
      ok: true,
      json: async () => {
        throw new Error('invalid JSON');
      },
    }),
  ]) {
    const gateway = loadGateway(fetch);
    await assert.rejects(gateway.fetchPlatformGatewayConfig());
    assert.deepEqual(snapshot(gateway.getGatewayConfig()), { endpoint: '', baseUrl: '' });
  }
});
