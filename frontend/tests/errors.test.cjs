const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const { QueryClient, QueryObserver } = require('@tanstack/react-query');

function loadQueries(apiClient) {
  const filename = path.resolve(__dirname, '../src/features/errors/errorQuery.ts');
  const source = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS },
  }).outputText;
  const loaded = { exports: {} };
  vm.runInNewContext(
    source,
    {
      module: loaded,
      exports: loaded.exports,
      require: name => (name === '../../api/client' ? { apiClient } : require(name)),
    },
    { filename },
  );
  return loaded.exports;
}

test('error time presets default to five minutes and never offer more than 24 hours', () => {
  const { ERROR_WINDOW_MINUTES, errorWindowMinutes } = loadQueries({});
  assert.deepEqual([...ERROR_WINDOW_MINUTES], [5, 15, 30, 60, 120, 360, 720, 1440]);
  for (const minutes of ERROR_WINDOW_MINUTES) {
    assert.equal(errorWindowMinutes(String(minutes)), minutes);
  }
  for (const input of [null, '', '0', '-5', '6', '1441', '2880', 'NaN', '5.5']) {
    assert.equal(errorWindowMinutes(input), 5, String(input));
  }
});

test('queries retain the time anchor across pages and send tenant and code filters', async context => {
  const requests = [];
  const { errorQueryOptions } = loadQueries({
    get: async (url, config) => {
      requests.push({ url, config });
      return { data: { data: { items: [], next_cursor: 'next' } } };
    },
  });
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity } } });
  context.after(() => client.clear());
  const filter = {
    window_minutes: 1440,
    to: '2026-11-03T04:00:00.000Z',
    limit: 20,
    organization_id: '11',
    key_id: '22',
    error_code: 'upstream_error',
  };
  await client.fetchQuery(errorQueryOptions(filter));
  await client.fetchQuery(errorQueryOptions({ ...filter, cursor: 'next' }));
  assert.equal(requests.length, 2);
  for (const request of requests) {
    assert.equal(request.url, '/admin/errors');
    assert.equal(request.config.params.to, filter.to);
    assert.equal(request.config.params.window_minutes, 1440);
    assert.equal(request.config.params.organization_id, '11');
    assert.equal(request.config.params.key_id, '22');
    assert.equal(request.config.params.error_code, 'upstream_error');
    assert.ok(request.config.signal);
  }
  assert.equal(requests[1].config.params.cursor, 'next');
  assert.notDeepEqual(
    [...errorQueryOptions(filter).queryKey],
    [...errorQueryOptions({ ...filter, to: '2026-11-03T05:00:00.000Z' }).queryKey],
  );
});

test('changing the time preset cancels the old request and leaving cancels the current request', context => {
  const requests = [];
  const { errorQueryOptions } = loadQueries({
    get: (url, config) => {
      requests.push({ url, config });
      return new Promise(() => {});
    },
  });
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity } } });
  context.after(() => client.clear());
  const filter = { window_minutes: 5, to: '2026-11-03T04:00:00.000Z', limit: 20 };
  const observer = new QueryObserver(client, errorQueryOptions(filter));
  const unsubscribe = observer.subscribe(() => {});
  assert.equal(requests.length, 1);
  observer.setOptions(errorQueryOptions({ ...filter, window_minutes: 15 }));
  assert.equal(requests.length, 2);
  assert.equal(requests[0].config.signal.aborted, true);
  assert.equal(requests[1].config.signal.aborted, false);
  unsubscribe();
  assert.equal(requests[1].config.signal.aborted, true);
});

test('timeouts are not automatically retried even when the application enables retries', async context => {
  let calls = 0;
  const { errorQueryOptions } = loadQueries({
    get: async () => {
      calls++;
      throw new Error('query timed out');
    },
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: 1, retryDelay: 0, gcTime: Infinity } },
  });
  context.after(() => client.clear());
  await assert.rejects(
    client.fetchQuery(
      errorQueryOptions({ window_minutes: 5, to: '2026-11-03T04:00:00.000Z', limit: 20 }),
    ),
    /query timed out/,
  );
  assert.equal(calls, 1);
});
