const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');
const ts = require('typescript');
const dayjs = require('dayjs');
const root = path.resolve(__dirname, '..');
const filename = path.join(root, 'src/features/keys/KeyExpiryInput.tsx');
const source = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
  compilerOptions: {
    target: ts.ScriptTarget.ES2020,
    module: ts.ModuleKind.CommonJS,
    jsx: ts.JsxEmit.React,
    esModuleInterop: true,
  },
}).outputText;
const loaded = { exports: {} };
vm.runInNewContext(
  source,
  { module: loaded, exports: loaded.exports, require: createRequire(filename) },
  { filename },
);
const { KeyExpiryInput } = loaded.exports;
const originalTZ = process.env.TZ;
process.env.TZ = 'Asia/Shanghai';
after(() => {
  if (originalTZ === undefined) delete process.env.TZ;
  else process.env.TZ = originalTZ;
});

test('the themed picker displays persisted expiry in local time without losing seconds', () => {
  const picker = KeyExpiryInput({ value: '2026-10-20T02:10:55Z' });
  assert.equal(picker.props.value.format('YYYY-MM-DD HH:mm:ss'), '2026-10-20 10:10:55');
});

test('choosing a local expiry saves the same instant as UTC and clearing preserves no expiry', () => {
  const changes = [];
  const picker = KeyExpiryInput({ onChange: value => changes.push(value) });
  picker.props.onChange(dayjs('2026-10-20 18:25:36'));
  picker.props.onChange(null);
  assert.deepEqual(changes, ['2026-10-20T10:25:36.000Z', null]);
});

test('empty and malformed stored values leave the picker empty instead of displaying an invalid date', () => {
  for (const value of [undefined, null, '', 'invalid-date'])
    assert.equal(KeyExpiryInput({ value }).props.value, null);
});
