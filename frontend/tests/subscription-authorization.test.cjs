const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

const filename = path.resolve(__dirname, '../src/features/providers/SubscriptionAuthorization.tsx');
const source = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    jsx: ts.JsxEmit.ReactJSX,
  },
}).outputText;

function createPopup() {
  const popup = {
    opener: {},
    closed: false,
    destinations: [],
    location: {
      replace(url) {
        popup.destinations.push(url);
      },
    },
    close() {
      this.closed = true;
    },
  };
  return popup;
}

function mount({ platform = 'openai', value, popup = createPopup(), open } = {}) {
  const events = [];
  const requests = [];
  const changes = [];
  const warnings = [];
  const busy = [];
  let cleanup;
  const exports = {};
  const jsx = (type, props) => ({ type, props });
  vm.runInNewContext(
    source,
    {
      exports,
      AbortController,
      window: {
        open: (...args) => {
          events.push(['open', ...args]);
          return open ? open() : popup;
        },
      },
      require: name => {
        if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx };
        if (name === 'react')
          return {
            useEffect: effect => {
              cleanup = effect();
            },
            useRef: () => ({}),
            useState: initial => [initial, value => busy.push(value)],
          };
        if (name === 'antd')
          return {
            Alert: 'Alert',
            Button: 'Button',
            Input: { TextArea: 'TextArea' },
            Space: 'Space',
            Typography: { Text: 'Text' },
            message: { warning: text => warnings.push(text) },
          };
        if (name === '@ant-design/icons') return {};
        if (name === '../../api/client')
          return {
            apiClient: {
              post: (url, body, { signal }) => {
                events.push(['post', url, popup?.opener]);
                return new Promise((resolve, reject) => {
                  requests.push({ url, body, signal, resolve, reject });
                });
              },
            },
          };
        if (name === '../../i18n') return { useI18n: () => ({ t: (key, fallback) => fallback }) };
        if (name === '../common/copyText') return {};
        throw new Error(`Unexpected authorization dependency: ${name}`);
      },
    },
    { filename },
  );
  const tree = exports.SubscriptionAuthorization({
    platform,
    connected: false,
    value,
    onChange: change => changes.push(change),
  });
  const button = tree.props.children[1].props.children;
  assert.equal(button.type, 'Button');
  assert.equal(button.props.type, 'primary');
  return { start: button.props.onClick, cleanup, events, requests, changes, warnings, busy, popup };
}

function session(platform = 'openai') {
  return {
    session_id: `${platform}-session`,
    auth_url: `https://example.invalid/oauth/${platform}?state=test`,
    state: 'test',
    expires_at: '2026-10-27T12:10:00Z',
  };
}

function resolve(request, data = session()) {
  request.resolve({ data: { data } });
}

for (const platform of ['openai', 'anthropic', 'gemini', 'antigravity']) {
  test(`${platform} click reserves a tab before the request and opens the returned sign-in URL`, async () => {
    const ui = mount({ platform, value: { project_id: 'existing-project' } });
    const pending = ui.start();
    assert.deepEqual(ui.events, [
      ['open', 'about:blank', '_blank'],
      ['post', `/admin/providers/oauth/${platform}/start`, null],
    ]);
    assert.deepEqual(ui.popup.destinations, []);
    resolve(ui.requests[0], session(platform));
    await pending;
    assert.deepEqual(ui.popup.destinations, [session(platform).auth_url]);
    assert.equal(ui.changes[0].session_id, session(platform).session_id);
    assert.equal(ui.changes[0].callback, '');
    assert.equal(ui.changes[0].project_id, 'existing-project');
    assert.deepEqual(ui.busy, [true, false]);
    assert.deepEqual(ui.warnings, []);
    ui.cleanup();
    assert.equal(ui.popup.closed, false, 'unmount must preserve an opened sign-in page');
  });
}

test('blocked popups preserve the generated link and explain how to open it', async () => {
  const ui = mount({ popup: null });
  const pending = ui.start();
  resolve(ui.requests[0]);
  await pending;
  assert.equal(ui.changes[0].auth_url, session().auth_url);
  assert.equal(ui.warnings.length, 1);
  assert.match(ui.warnings[0], /Open official sign-in page/);
  assert.deepEqual(ui.busy, [true, false]);
});

test('window.open failures still allow the link to be generated', async () => {
  const ui = mount({
    open: () => {
      throw new Error('Opening a tab is unavailable');
    },
  });
  const pending = ui.start();
  resolve(ui.requests[0]);
  await pending;
  assert.equal(ui.changes[0].auth_url, session().auth_url);
  assert.equal(ui.warnings.length, 1);
});

test('a tab closed while generating the link offers manual sign-in', async () => {
  const ui = mount();
  const pending = ui.start();
  ui.popup.close();
  resolve(ui.requests[0]);
  await pending;
  assert.deepEqual(ui.popup.destinations, []);
  assert.equal(ui.changes[0].auth_url, session().auth_url);
  assert.equal(ui.warnings.length, 1);
});

test('navigation failures close the blank tab and preserve the sign-in link', async () => {
  const ui = mount();
  ui.popup.location.replace = () => {
    throw new Error('Navigation blocked');
  };
  const pending = ui.start();
  resolve(ui.requests[0]);
  await pending;
  assert.equal(ui.popup.closed, true);
  assert.equal(ui.changes[0].auth_url, session().auth_url);
  assert.equal(ui.warnings.length, 1);
});

test('failed requests close the reserved tab without replacing the existing form', async () => {
  const ui = mount();
  const pending = ui.start();
  ui.requests[0].reject(new Error('OAuth client is not configured'));
  await pending;
  assert.equal(ui.popup.closed, true);
  assert.deepEqual(ui.changes, []);
  assert.deepEqual(ui.warnings, []);
  assert.deepEqual(ui.busy, [true, false]);
});

test('unmounting or switching platforms cancels the request and closes the blank tab', async () => {
  const ui = mount();
  const pending = ui.start();
  ui.cleanup();
  assert.equal(ui.requests[0].signal.aborted, true);
  assert.equal(ui.popup.closed, true);
  resolve(ui.requests[0]);
  await pending;
  assert.deepEqual(ui.changes, []);
  assert.deepEqual(ui.popup.destinations, []);
  assert.deepEqual(ui.warnings, []);
  assert.deepEqual(ui.busy, [true]);
});

test('restarting cancels the previous attempt without closing the new tab', async () => {
  const popups = [createPopup(), createPopup()];
  let count = 0;
  const ui = mount({ open: () => popups[count++] });
  const first = ui.start();
  const second = ui.start();
  assert.equal(ui.requests[0].signal.aborted, true);
  assert.equal(popups[0].closed, true);
  assert.equal(popups[1].closed, false);
  resolve(ui.requests[0]);
  await first;
  assert.deepEqual(ui.changes, []);
  resolve(ui.requests[1], { ...session(), session_id: 'new-session' });
  await second;
  assert.equal(ui.changes.length, 1);
  assert.equal(ui.changes[0].session_id, 'new-session');
  assert.deepEqual(popups[0].destinations, []);
  assert.deepEqual(popups[1].destinations, [session().auth_url]);
  assert.equal(popups[1].closed, false);
  assert.deepEqual(ui.busy, [true, true, false]);
});
