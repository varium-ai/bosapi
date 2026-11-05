const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

const root = path.resolve(__dirname, '../src');
function parse(relative) {
  return ts.createSourceFile(
    relative,
    fs.readFileSync(path.join(root, relative), 'utf8'),
    ts.ScriptTarget.Latest,
    true,
    relative.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}
function find(tree, predicate) {
  let found;
  function visit(node) {
    if (predicate(node)) found = node;
    else ts.forEachChild(node, visit);
  }
  visit(tree);
  assert.ok(found, 'Expected source declaration');
  return found;
}
function evaluate(source, globals = {}) {
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2020, jsx: ts.JsxEmit.React },
  }).outputText;
  return vm.runInNewContext(code, globals);
}
const tree = parse('features/organizations/OrganizationsPage.tsx');
const providerQuery = find(
  tree,
  node =>
    ts.isCallExpression(node) &&
    node.expression.getText(tree) === 'useQuery' &&
    node.arguments[0]?.getText(tree).includes("'providers-list'"),
).arguments[0];
const column = find(
  tree,
  node =>
    ts.isObjectLiteralExpression(node) &&
    node.properties.some(
      property =>
        property.name?.getText(tree) === 'dataIndex' &&
        property.initializer?.getText(tree) === "'allowed_provider_ids'",
    ),
);
const renderSource = column.properties
  .find(property => property.name?.getText(tree) === 'render')
  .initializer.getText(tree);
function query(currentUser, get = async () => {}) {
  const globals = { apiClient: { get } };
  for (const [relative, name] of [
    ['features/onboarding/firstKey.ts', 'collectSetupPages'],
    ['features/onboarding/setupQueries.ts', 'loadSetupPages'],
  ]) {
    const sourceTree = parse(relative);
    const declaration = find(
      sourceTree,
      node => ts.isFunctionDeclaration(node) && node.name?.text === name,
    );
    globals[name] = evaluate(
      `${declaration.getText(sourceTree).replace(/^export /, '')}\n${name};`,
      globals,
    );
  }
  return evaluate(`(${providerQuery.getText(tree)});`, {
    ...globals,
    currentUser,
    isPlatformAdmin: currentUser?.role === 'super_admin',
  });
}
function render(ids, providersData, providersError = false) {
  const renderer = evaluate(`(${renderSource});`, {
    providersData,
    providersError,
    t: key => key,
    Text: 'Text',
    ListSummary: 'ListSummary',
    React: { createElement: (type, props, ...children) => ({ type, props, children }) },
  });
  return renderer(ids);
}

test('organization and platform admins can resolve channel names; other roles cannot query', () => {
  assert.equal(query({ id: 1, role: 'super_admin' }).enabled, true);
  assert.equal(query({ id: 2, role: 'org_admin', organization_id: 3 }).enabled, true);
  for (const currentUser of [null, { role: 'key_viewer' }, { role: 'unknown' }]) {
    assert.ok(!query(currentUser).enabled);
  }
});

test('channel query cache is isolated by account, role and organization', () => {
  const user = { id: 2, role: 'org_admin', organization_id: 3 };
  const key = JSON.stringify(query(user).queryKey);
  for (const change of [{ id: 4 }, { role: 'super_admin' }, { organization_id: 5 }]) {
    assert.notEqual(JSON.stringify(query({ ...user, ...change }).queryKey), key);
  }
});

test('channel names load beyond the first 100 results', async () => {
  const providers = Array.from({ length: 102 }, (_, index) => ({
    id: index + 1,
    name: `Channel ${index + 1}`,
  }));
  const requests = [];
  const signal = new AbortController().signal;
  const options = query({ id: 2, role: 'org_admin', organization_id: 3 }, async (url, config) => {
    requests.push({ url, ...config });
    const start = (config.params.page - 1) * config.params.page_size;
    return { data: { data: { items: providers.slice(start, start + 100), total: 102 } } };
  });
  const data = await options.queryFn({ signal });
  assert.equal(requests.length, 2);
  requests.forEach((request, index) => {
    assert.equal(request.url, '/admin/providers');
    assert.equal(request.signal, signal);
    assert.equal(request.params.page, index + 1);
    assert.equal(request.params.page_size, 100);
    assert.equal(request.params.organization_id, undefined);
  });
  assert.equal(data.length, 102);
  const cell = render([1, 101, 102], data);
  assert.deepEqual(Array.from(cell.props.items), ['Channel 1', 'Channel 101', 'Channel 102']);
});

test('failed or cancelled channel requests propagate instead of returning an empty list', async () => {
  for (const error of [new Error('Unavailable'), new Error('Cancelled')]) {
    const signal = new AbortController().signal;
    const options = query({ role: 'super_admin' }, async (_, config) => {
      assert.equal(config.signal, signal);
      throw error;
    });
    await assert.rejects(options.queryFn({ signal }), actual => actual === error);
    assert.equal(options.retry, false);
  }
});

test('channel cells show one loading or error message instead of repeated unavailable labels', () => {
  const loading = render([1, 2, 3], undefined);
  assert.equal(loading.type, 'Text');
  assert.deepEqual(loading.children, ['common.loading']);
  const failed = render([1, 2, 3], undefined, true);
  assert.equal(failed.type, 'Text');
  assert.deepEqual(failed.children, ['organizations.provider_names_load_error']);
  const empty = render([], undefined);
  assert.equal(empty.type, 'ListSummary');
  assert.equal(empty.props.items.length, 0);
});

test('known channel names survive refresh errors; genuinely missing names use the fallback', () => {
  const data = [{ id: 1, name: 'Assigned channel' }];
  assert.deepEqual(Array.from(render([1], data, true).props.items), ['Assigned channel']);
  assert.deepEqual(Array.from(render([1, 2], data).props.items), [
    'Assigned channel',
    'ux.unnamed_provider',
  ]);
  assert.deepEqual(Array.from(render([2], data, true).props.items), [
    'organizations.provider_names_load_error',
  ]);
});

test('organization limits link targets the selected organization and is tenant-scoped', () => {
  const link = find(
    tree,
    node =>
      ts.isJsxElement(node) &&
      node.openingElement.tagName.getText(tree) === 'Link' &&
      node.getText(tree).includes('/admin/rate-limits?organization_id='),
  );
  let condition = link.parent;
  while (!ts.isBinaryExpression(condition)) condition = condition.parent;
  const render = (currentUser, record) =>
    evaluate(`(${condition.getText(tree)});`, {
      currentUser,
      record,
      isPlatformAdmin: currentUser?.role === 'super_admin',
      Link: 'Link',
      t: key => key,
      React: { createElement: (type, props, ...children) => ({ type, props, children }) },
    });
  const admin = render({ role: 'super_admin' }, { id: 25 });
  assert.equal(admin.props.to, '/admin/rate-limits?organization_id=25');
  assert.deepEqual(Array.from(admin.children), ['ratelimits.org_limits_link']);
  const tenant = { role: 'org_admin', organization_id: 10 };
  const own = render(tenant, { id: 10 });
  assert.equal(own.props.to, '/admin/rate-limits?organization_id=10');
  assert.deepEqual(Array.from(own.children), ['ratelimits.org_view_limits_link']);
  assert.equal(render(tenant, { id: 25 }), false);
  assert.equal(render({ role: 'key_viewer' }, { id: 10 }), false);
});
