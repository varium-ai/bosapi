const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');

test('production build removes nested desktop metadata and preserves public assets', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'bos-build-output-')));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const pluginPath = path.resolve(__dirname, '../coldStartPlugin.ts');
  const source = ts.transpileModule(await fs.readFile(pluginPath, 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
      esModuleInterop: true,
    },
  }).outputText;
  const loaded = { exports: {} };
  vm.runInNewContext(
    source,
    { module: loaded, exports: loaded.exports, require },
    { filename: pluginPath },
  );
  const metadata = [
    '.DS_Store',
    'Thumbs.db',
    '._logo.svg',
    'docs/.DS_Store',
    'docs/legal/._guide.md',
  ];
  const publicFiles = {
    'logo.svg': '<svg></svg>',
    'docs/legal/guide.en.md': '# Legal document',
    'docs/brands/sources.json': '{"source":"fixture"}',
    // Other hidden files stay visible to the strict publication validator.
    'docs/.credentials': 'fixture',
    'docs/._directory/guide.md': '# Keep unknown directories',
  };
  for (const [name, contents] of Object.entries({
    ...Object.fromEntries(metadata.map(name => [name, 'desktop metadata'])),
    ...publicFiles,
  })) {
    const target = path.join(root, 'public', name);
    await fs.mkdir(path.dirname(target), { recursive: true });
    await fs.writeFile(target, contents);
  }
  await fs.writeFile(
    path.join(root, 'index.html'),
    '<!doctype html><html><head><title>Fixture</title></head><body><script type="module" src="/main.js"></script></body></html>',
  );
  await fs.writeFile(
    path.join(root, 'main.js'),
    `console.log(${JSON.stringify('fixture'.repeat(100))});`,
  );
  const { build } = await import('vite');
  await build({
    configFile: false,
    root,
    logLevel: 'silent',
    plugins: [loaded.exports.coldStartPlugin()],
    build: { minify: false },
  });
  for (const name of metadata) {
    await assert.rejects(fs.access(path.join(root, 'dist', name)), { code: 'ENOENT' });
    assert.equal(await fs.readFile(path.join(root, 'public', name), 'utf8'), 'desktop metadata');
  }
  for (const [name, contents] of Object.entries(publicFiles)) {
    assert.equal(await fs.readFile(path.join(root, 'dist', name), 'utf8'), contents);
  }
  const index = await fs.readFile(path.join(root, 'dist/index.html'), 'utf8');
  assert.match(index, /modulepreload/);
  const assets = await fs.readdir(path.join(root, 'dist/assets'));
  assert.ok(assets.some(name => name.endsWith('.js.gz')));
  assert.ok(assets.some(name => name.endsWith('.js.br')));
});
