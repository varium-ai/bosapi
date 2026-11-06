const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');

const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(root, 'node_modules/.announcement-test-'));
const source = fs.readFileSync(
  path.join(root, 'src/features/announcements/AnnouncementMarkdown.tsx'),
  'utf8',
);
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.ReactJSX },
}).outputText;
const output = path.join(temporary, 'AnnouncementMarkdown.mjs');
fs.writeFileSync(output, compiled);
const component = import(pathToFileURL(output).href);
after(() => fs.rmSync(temporary, { recursive: true, force: true }));
const render = async body => {
  const { AnnouncementMarkdown } = await component;
  return renderToStaticMarkup(React.createElement(AnnouncementMarkdown, { body }));
};

test('announcements render Markdown headings, emphasis, lists, code and GFM tables', async () => {
  const html = await render(
    '# Release\n\n**Bold** and *emphasis*\n\n- One\n- Two\n\n```js\nconst a = 1;\n```\n\n| Name | Value |\n| --- | --- |\n| Item | 1 |',
  );
  for (const expected of [
    '<h1>Release</h1>',
    '<strong>Bold</strong>',
    '<em>emphasis</em>',
    '<li>One</li>',
    '<pre><code class="language-js">',
    '<table>',
    '<td>Item</td>',
  ]) {
    assert.ok(html.includes(expected), expected);
  }
});

test('announcement Markdown omits raw HTML and unsafe link protocols', async () => {
  const html = await render(
    '<script>alert(1)</script>\n\n<img src=x onerror="alert(1)">\n\n[unsafe](javascript:alert%281%29)\n\n![unsafe](javascript:alert%281%29)',
  );
  assert.doesNotMatch(html, /<script|onerror|javascript:/);
  assert.match(html, /unsafe/);
});

test('announcement links open safely and code remains escaped', async () => {
  const html = await render('[Docs](https://example.com/docs)\n\n`<script>alert(1)</script>`');
  assert.match(html, /href="https:\/\/example.com\/docs"/);
  assert.match(html, /target="_blank" rel="noopener noreferrer"/);
  assert.match(html, /&lt;script&gt;/);
  assert.doesNotMatch(html, /<script>/);
});

function loadData(get = async () => ({ data: { data: { items: [], total: 0 } } })) {
  const Module = require('node:module');
  const file = path.join(root, 'src/features/announcements/data.ts');
  const data = new Module(file, module);
  data.require = name => {
    if (name === '@tanstack/react-query') return { useQuery: options => options };
    if (name === '../../api/client') return { apiClient: { get } };
    return require(name);
  };
  data._compile(
    ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS },
    }).outputText,
    file,
  );
  return data.exports;
}

function renderPanel(query, management = false) {
  const Module = require('node:module');
  const file = path.join(
    root,
    `src/features/announcements/${management ? 'AnnouncementsPage' : 'AnnouncementPanel'}.tsx`,
  );
  const panel = new Module(file, module);
  panel.filename = file;
  panel.paths = module.paths;
  panel.require = name => {
    if (name === './data') return { ...loadData(), useAnnouncements: () => query };
    if (name === 'react-router-dom')
      return {
        useOutletContext: () => ({ user: { role: 'super_admin', organization_id: 0, id: 1 } }),
      };
    if (name === '@tanstack/react-query')
      return {
        useQueryClient: () => ({ invalidateQueries() {} }),
        useMutation: () => ({ reset() {} }),
      };
    if (name === '../../api/client') return { apiClient: {} };
    if (name === '../../i18n')
      return { useI18n: () => ({ t: key => key, formatDateTime: date => date }) };
    if (name === './AnnouncementMarkdown')
      return { AnnouncementMarkdown: ({ body }) => React.createElement('p', null, body) };
    if (name.endsWith('.css')) return {};
    return require(name);
  };
  panel._compile(
    ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
    }).outputText,
    file,
  );
  return renderToStaticMarkup(
    React.createElement(
      management ? panel.exports.AnnouncementsPage : panel.exports.AnnouncementPanel,
      {
        scope: 'super_admin:1:1',
        superAdmin: true,
      },
    ),
  );
}

test('overview announcements remain read-only for super administrators', () => {
  const html = renderPanel({
    data: {
      items: [
        {
          id: 7,
          title: 'Revised announcement',
          body: '# Updated body',
          created_at: '2026-10-30T08:00:00Z',
        },
      ],
      total: 1,
    },
  });
  assert.match(html, /Revised announcement/);
  assert.match(html, /# Updated body/);
  assert.doesNotMatch(html, /<button|<form|announcements.create|common.edit|common.delete/);
});

test('announcement management and overview distinguish empty, unavailable and failed reads', () => {
  for (const management of [false, true]) {
    const emptyKey = management ? 'announcements.not_created' : 'announcements.empty';
    const empty = renderPanel({ data: { items: [], total: 0 } }, management);
    assert.ok(empty.includes(emptyKey));
    assert.doesNotMatch(empty, /announcements.load_failed|announcements.unavailable|common.retry/);
    for (const status of [404, 401, 403, 500, undefined]) {
      const failed = renderPanel(
        { isError: true, error: { response: { status } }, data: { items: [], total: 0 } },
        management,
      );
      assert.ok(
        failed.includes(status === 404 ? 'announcements.unavailable' : 'announcements.load_failed'),
      );
      assert.match(failed, /common.retry/);
      assert.doesNotMatch(failed, /announcements.empty|announcements.not_created/);
    }
  }
});

test('overview distinguishes organization publications from global announcements', () => {
  const html = renderPanel({
    data: {
      items: [
        {
          id: 7,
          organization_id: 23,
          title: 'Organization notice',
          body: 'Private body',
          created_at: '2026-10-30T08:00:00Z',
        },
        {
          id: 8,
          organization_id: 0,
          title: 'Global notice',
          body: 'Global body',
          created_at: '2026-10-30T08:00:00Z',
        },
      ],
      total: 2,
    },
  });
  assert.match(html, /announcements.scope_organization/);
  assert.match(html, /announcements.scope_global/);
  assert.doesNotMatch(html, /<button|<form/);
});

test('announcement queries isolate management caches and use the protected list', async () => {
  const requests = [];
  const data = loadData(async (endpoint, config) => {
    requests.push({ endpoint, params: config.params });
    return { data: { data: { items: [], total: 0 } } };
  });
  const read = data.useAnnouncements('org_admin:23:1', 1);
  const manage = data.useAnnouncements('org_admin:23:1', 1, false, true);
  const key = data.useAnnouncements('key:7', 1, true);
  const other = data.useAnnouncements('org_admin:99:2', 1);
  assert.notDeepEqual(read.queryKey, manage.queryKey);
  assert.notDeepEqual(read.queryKey, other.queryKey);
  for (const query of [read, manage, key]) await query.queryFn({});
  assert.deepEqual(
    requests.map(request => request.endpoint),
    ['/admin/announcements', '/admin/announcements/manage', '/key-portal/announcements'],
  );
  assert.ok(requests.every(request => !Object.hasOwn(request.params, 'organization_id')));
});

test('empty announcement payloads normalize without treating malformed data or HTTP errors as empty', async () => {
  let payload;
  let failure;
  const data = loadData(async () => {
    if (failure) throw failure;
    return { data: { data: payload } };
  });
  const query = data.useAnnouncements('super_admin:0:1', 1, false, true);
  for (const items of [[], null]) {
    payload = { items, total: 0 };
    assert.deepEqual(await query.queryFn({}), { items: [], total: 0 });
  }
  for (payload of [null, {}, { items: null, total: 1 }, { items: [], total: -1 }])
    await assert.rejects(query.queryFn({}), /Invalid announcement list response/);
  for (const status of [404, 500]) {
    failure = { response: { status } };
    await assert.rejects(query.queryFn({}), error => error === failure);
  }
});
