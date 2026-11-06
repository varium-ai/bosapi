const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

test('CSS and Ant Design share the same semantic console palette', () => {
  const root = path.resolve(__dirname, '../src');
  const source = fs.readFileSync(path.join(root, 'theme/palette.ts'), 'utf8');
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const exports = {};
  vm.runInNewContext(compiled, { exports });

  const css = fs.readFileSync(path.join(root, 'style.css'), 'utf8');
  const declarations = css.match(/:root\s*\{([^}]+)\}/)?.[1];
  assert.ok(declarations, 'The global console palette must be declared in :root.');
  const variables = new Map(
    [...declarations.matchAll(/--([\w-]+):\s*(#[\da-f]+);/gi)].map(([, name, value]) => [
      name,
      value.toLowerCase(),
    ]),
  );
  const aliases = { divider: 'line-light', ring: 'focus-ring' };
  for (const [token, value] of Object.entries(exports.lightPalette)) {
    const variable =
      aliases[token] || token.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`);
    assert.equal(variables.get(variable), value, `${token} must match --${variable}`);
  }
});
