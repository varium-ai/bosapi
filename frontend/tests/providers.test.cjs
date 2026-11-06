const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

const source = fs.readFileSync(
  path.resolve(__dirname, '../src/features/providers/ProvidersPage.tsx'),
  'utf8',
);
const tree = ts.createSourceFile(
  'ProvidersPage.tsx',
  source,
  ts.ScriptTarget.Latest,
  true,
  ts.ScriptKind.TSX,
);
const declarations = new Map();
function visit(node) {
  if (
    ts.isVariableDeclaration(node) &&
    [
      'PLATFORM_DEFAULTS',
      'handlePlatformChange',
      'saveMutation',
      'discoverModels',
      'handleFetchUpstream',
    ].includes(node.name.getText(tree))
  ) {
    declarations.set(node.name.getText(tree), node.getText(tree));
  }
  ts.forEachChild(node, visit);
}
visit(tree);

function switchPlatform(values, platform, editingProvider = null) {
  const fields = { ...values };
  const code = ts.transpileModule(
    `const ${declarations.get('PLATFORM_DEFAULTS')}; const ${declarations.get('handlePlatformChange')}; handlePlatformChange(platform);`,
    { compilerOptions: { target: ts.ScriptTarget.ES2020 } },
  ).outputText;
  vm.runInNewContext(code, {
    selectedPlatform: values.platform,
    platform,
    editingProvider,
    form: {
      getFieldValue: name => fields[name],
      setFieldValue: (name, value) => {
        fields[name] = value;
      },
    },
  });
  fields.platform = platform;
  return fields;
}
const initial = {
  platform: 'openai',
  base_url: 'https://api.openai.com/v1',
  api_format: 'openai_chat',
};

test('platform selection updates defaults across repeated switches', () => {
  assert.match(source, /onChange=\{handlePlatformChange\}/);
  const anthropic = switchPlatform(initial, 'anthropic');
  assert.equal(anthropic.base_url, 'https://api.anthropic.com');
  assert.equal(anthropic.api_format, 'anthropic_messages');
  const gemini = switchPlatform(anthropic, 'gemini');
  assert.equal(gemini.base_url, 'https://generativelanguage.googleapis.com');
  assert.equal(gemini.api_format, 'gemini');
  assert.deepEqual(switchPlatform(gemini, 'openai'), initial);
});

test('platform selection preserves custom endpoint paths and explicit protocols', () => {
  const values = {
    ...initial,
    base_url: 'https://proxy.example.com/custom/anthropic',
    api_format: 'openai_responses',
  };
  const result = switchPlatform(values, 'anthropic');
  assert.equal(result.base_url, values.base_url);
  assert.equal(result.api_format, values.api_format);
});

test('editing channels keeps saved settings', () => {
  const result = switchPlatform(initial, 'anthropic', { id: 1 });
  assert.equal(result.base_url, initial.base_url);
  assert.equal(result.api_format, initial.api_format);
});

test('platforms requiring custom endpoints clear the previous default', () => {
  for (const platform of ['antigravity', 'composite']) {
    const result = switchPlatform(initial, platform);
    assert.equal(result.base_url, '');
    assert.equal(switchPlatform(result, 'anthropic').base_url, 'https://api.anthropic.com');
  }
});

function providerSaveFlow({
  models = [{ model_name: 'account-model' }],
  failDiscovery = false,
  accountType = 'oauth',
} = {}) {
  const events = [];
  const mutations = [];
  const values = {
    name: 'Subscription',
    platform: 'openai',
    account_type: accountType,
    models: [],
    oauth_authorization: { session_id: 'session', callback: ' callback ', state: 'state' },
  };
  const saved = { id: 7, name: values.name, account_type: accountType };
  const code = ts.transpileModule(
    ['saveMutation', 'discoverModels', 'handleFetchUpstream']
      .map(name => `const ${declarations.get(name)};`)
      .join('\n') + '\n({ saveMutation, discoverModels, handleFetchUpstream });',
    { compilerOptions: { target: ts.ScriptTarget.ES2020 } },
  ).outputText;
  const handlers = vm.runInNewContext(code, {
    useMutation: options => options,
    saveDefaultPricing: (existing, input, output, cacheRead, cacheWrite) => ({
      ...existing,
      input,
      output,
      cache_read: cacheRead,
      cache_write: cacheWrite,
    }),
    editingProvider: null,
    mappingMode: 'visual',
    mappingRows: [],
    isSubscription: accountType === 'oauth',
    setModalVisible: open => events.push(['modal', open]),
    setSelectedProvider: provider => events.push(['selected', provider.id]),
    setModelsDrawerVisible: open => events.push(['drawer', open]),
    setDiscoveringId: id => events.push(['discovering', id]),
    setIsFetchingUpstream: value => events.push(['fetching', value]),
    form: {
      resetFields: () => events.push(['reset']),
      validateFields: async () => values,
    },
    queryClient: { invalidateQueries: () => {} },
    invalidateFirstKeySetup: () => {},
    t: (key, fallback) => fallback || key,
    message: {
      success: text => events.push(['success', text]),
      warning: text => events.push(['warning', text]),
    },
    apiClient: {
      post: async (url, payload) => {
        events.push(['post', url, payload]);
        if (url.endsWith('/complete') || url === '/admin/providers')
          return { data: { data: saved } };
        if (failDiscovery) throw new Error('Model discovery unavailable');
        return { data: { data: { items: models } } };
      },
    },
  });
  handlers.saveMutation.mutate = input => mutations.push(input);
  return {
    ...handlers,
    values,
    saved,
    events,
    mutations,
    save: async (input = values) => {
      const response = await handlers.saveMutation.mutationFn(input);
      await handlers.saveMutation.onSuccess(response, input);
    },
  };
}

test('saving an authorized account with no models completes OAuth before discovering models', async () => {
  const ui = providerSaveFlow();
  await ui.save();
  const requests = ui.events.filter(event => event[0] === 'post');
  assert.equal(requests[0][1], '/admin/providers/oauth/openai/complete');
  assert.equal(requests[0][2].callback, 'callback');
  assert.equal(requests[0][2].provider.models.length, 0);
  assert.equal(requests[1][1], '/admin/providers/7/discover-models');
  assert.ok(ui.events.some(event => event[0] === 'modal' && event[1] === false));
  assert.ok(ui.events.some(event => event[0] === 'drawer' && event[1] === true));
  assert.equal(ui.events.filter(event => event[0] === 'warning').length, 0);
});

for (const failDiscovery of [false, true]) {
  test(`a saved account remains saved when discovery ${failDiscovery ? 'fails' : 'returns no models'}`, async () => {
    const ui = providerSaveFlow({ models: [], failDiscovery });
    await ui.save();
    assert.equal(ui.events.filter(event => event[0] === 'post').length, 2);
    assert.ok(ui.events.some(event => event[0] === 'success' && /saved/.test(event[1])));
    assert.ok(
      ui.events.some(
        event =>
          event[0] === 'warning' &&
          (failDiscovery
            ? event[1] === 'qa.saved_discovery_failed'
            : /Edit Provider/.test(event[1])),
      ),
    );
    assert.ok(ui.events.some(event => event[0] === 'selected' && event[1] === 7));
    assert.ok(ui.events.some(event => event[0] === 'discovering' && event[1] === null));
  });
}

test('save-and-discover validates the form and submits an explicit discovery request', async () => {
  const ui = providerSaveFlow();
  await ui.handleFetchUpstream();
  assert.equal(ui.mutations.length, 1);
  assert.equal(ui.mutations[0].discover_models, true);
  assert.equal(ui.events.length, 0);
  await ui.save({ ...ui.values, models: ['manually-selected'], discover_models: true });
  assert.equal(ui.events.filter(event => event[0] === 'post').length, 2);
});

test('normal saving preserves explicitly selected subscription models without discovering others', async () => {
  const ui = providerSaveFlow();
  await ui.save({ ...ui.values, models: ['manually-selected'] });
  assert.equal(ui.events.filter(event => event[0] === 'post').length, 1);
  assert.equal(ui.events.filter(event => event[0] === 'drawer').length, 0);
});

for (const failDiscovery of [false, true]) {
  test(`API Key save-and-discover ${failDiscovery ? 'keeps the saved channel on discovery failure' : 'continues to the model drawer'}`, async () => {
    const ui = providerSaveFlow({ accountType: 'apikey', failDiscovery });
    await ui.handleFetchUpstream();
    assert.equal(ui.mutations.length, 1);
    await ui.save(ui.mutations[0]);
    const requests = ui.events.filter(event => event[0] === 'post');
    assert.deepEqual(
      requests.map(event => event[1]),
      ['/admin/providers', '/admin/providers/7/discover-models'],
    );
    assert.ok(ui.events.some(event => event[0] === 'selected' && event[1] === 7));
    assert.ok(ui.events.some(event => event[0] === 'drawer' && event[1]));
    assert.equal(
      ui.events.filter(event => event[0] === 'post' && event[1] === '/admin/providers').length,
      1,
    );
  });
}
