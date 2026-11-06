const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const { QueryClient } = require('@tanstack/react-query');

function loadSource(relativePath, dependencies = {}) {
  const filename = path.resolve(__dirname, relativePath);
  const source = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
  }).outputText;
  const loaded = { exports: {} };
  vm.runInNewContext(
    source,
    {
      module: loaded,
      exports: loaded.exports,
      require: name => dependencies[name] || require(name),
    },
    { filename },
  );
  return loaded.exports;
}

const { getSecurityEventSeverityFilter } = loadSource(
  '../src/features/security/securityEventSeverity.ts',
);
const account = { id: 1, organization_id: 12, role: 'org_admin' };
const envelope = unread_count => ({ data: { data: { unread_count } } });
const settle = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
};

function setup(context, apiClient) {
  const api = loadSource('../src/api/securityEvents.ts', { './client': { apiClient } });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  context.after(() => client.clear());
  return { ...api, client };
}

test('blank or reset severity filters include warnings, high and critical events by default', () => {
  for (const value of [null, '', 'invalid']) {
    assert.deepEqual(
      { ...getSecurityEventSeverityFilter(value) },
      {
        selection: 'warning_and_above',
        min_severity: 'warning',
      },
    );
  }
});

test('all severities removes the minimum while each exact level remains selectable', () => {
  assert.deepEqual({ ...getSecurityEventSeverityFilter('all') }, { selection: 'all' });
  for (const value of ['info', 'warning', 'high', 'critical']) {
    assert.deepEqual(
      { ...getSecurityEventSeverityFilter(value) },
      {
        selection: value,
        severity: value,
      },
    );
  }
});

test('unread queries isolate user, role and organization and only run for administrators', async context => {
  const requests = [];
  const { client, securityEventUnreadQueryOptions, securityEventUnreadQueryKey } = setup(context, {
    get: async (url, config) => {
      requests.push({ url, config });
      return envelope(4);
    },
  });
  const key = securityEventUnreadQueryKey(account);
  for (const other of [
    { ...account, id: 2 },
    { ...account, role: 'super_admin' },
    { ...account, organization_id: 13 },
  ]) {
    assert.notDeepEqual([...securityEventUnreadQueryKey(other)], [...key]);
  }
  for (const user of [
    null,
    { ...account, role: 'key_viewer' },
    { ...account, role: '' },
    { ...account, organization_id: 0 },
    { id: 1, role: 'org_admin' },
  ]) {
    const options = securityEventUnreadQueryOptions(user);
    assert.equal(options.enabled, false);
    assert.equal(await client.fetchQuery(options), 0);
  }
  assert.equal(requests.length, 0);
  const options = securityEventUnreadQueryOptions(account);
  assert.equal(options.enabled, true);
  assert.equal(options.refetchInterval, 15000);
  assert.equal(options.refetchIntervalInBackground, false);
  assert.equal(await client.fetchQuery(options), 4);
  assert.equal(requests[0].url, '/admin/security-events/unread');
  assert.ok(requests[0].config.signal);
  assert.equal(requests[0].config.silent, true);
});

test('super administrators without organization_id poll under the global account scope', async context => {
  const requests = [];
  const { client, securityEventUnreadQueryOptions, securityEventUnreadQueryKey } = setup(context, {
    get: async url => {
      requests.push(url);
      return envelope(2);
    },
  });
  const globalAdmin = { id: 1, role: 'super_admin' };
  const options = securityEventUnreadQueryOptions(globalAdmin);
  assert.equal(options.enabled, true);
  assert.deepEqual(
    [...securityEventUnreadQueryKey(globalAdmin)],
    ['security-events-unread', 1, 'super_admin', 0],
  );
  assert.deepEqual(
    [...securityEventUnreadQueryKey(globalAdmin)],
    [...securityEventUnreadQueryKey({ ...globalAdmin, organization_id: 0 })],
  );
  assert.equal(await client.fetchQuery(options), 2);
  assert.equal(requests.length, 1);
});

test('marking read cancels old polls before and during the write so stale counts cannot restore the badge', async context => {
  const gets = [];
  const write = deferred();
  const posts = [];
  const {
    client,
    securityEventUnreadQueryOptions,
    securityEventUnreadQueryKey,
    markSecurityEventsRead,
  } = setup(context, {
    get: (url, config) => {
      const response = deferred();
      gets.push({ url, config, ...response });
      return response.promise;
    },
    post: (url, body, config) => {
      posts.push({ url, body, config });
      return write.promise;
    },
  });
  const options = securityEventUnreadQueryOptions(account);
  const key = securityEventUnreadQueryKey(account);
  client.setQueryData(key, 3);
  void client.fetchQuery(options).catch(() => {});
  const read = markSecurityEventsRead(client, account, new AbortController().signal);
  await settle();
  assert.equal(gets[0].config.signal.aborted, true);
  assert.equal(posts[0].url, '/admin/security-events/read');
  assert.equal(posts[0].body, undefined);
  void client.fetchQuery(options).catch(() => {});
  write.resolve(envelope(0));
  await read;
  assert.equal(gets[1].config.signal.aborted, true);
  assert.equal(client.getQueryData(key), 0);
  gets[0].resolve(envelope(3));
  gets[1].resolve(envelope(3));
  await settle();
  assert.equal(client.getQueryData(key), 0);
  const poll = client.fetchQuery(options);
  gets[2].resolve(envelope(1));
  await poll;
  assert.equal(client.getQueryData(key), 1);
});

test('failed marking leaves the unread reminder and other account caches intact', async context => {
  const failure = new Error('write failed');
  const { client, securityEventUnreadQueryKey, markSecurityEventsRead } = setup(context, {
    post: async () => {
      throw failure;
    },
  });
  const key = securityEventUnreadQueryKey(account);
  const otherKey = securityEventUnreadQueryKey({ ...account, id: 2 });
  client.setQueryData(key, 3);
  client.setQueryData(otherKey, 7);
  await assert.rejects(
    markSecurityEventsRead(client, account, new AbortController().signal),
    failure,
  );
  assert.equal(client.getQueryData(key), 3);
  assert.equal(client.getQueryData(otherKey), 7);
});

test('account changes or unmounts abort a read without clearing either account cache', async context => {
  const write = deferred();
  const requests = [];
  const { client, securityEventUnreadQueryKey, markSecurityEventsRead } = setup(context, {
    post: (url, body, config) => {
      requests.push(config);
      return write.promise;
    },
  });
  const key = securityEventUnreadQueryKey(account);
  const otherKey = securityEventUnreadQueryKey({ ...account, id: 2 });
  client.setQueryData(key, 3);
  client.setQueryData(otherKey, 7);
  const controller = new AbortController();
  const read = markSecurityEventsRead(client, account, controller.signal);
  await settle();
  controller.abort();
  assert.equal(requests[0].signal.aborted, true);
  write.resolve(envelope(0));
  await read;
  assert.equal(client.getQueryData(key), 3);
  assert.equal(client.getQueryData(otherKey), 7);
  await markSecurityEventsRead(client, account, controller.signal);
  assert.equal(requests.length, 1);
});
