const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-key-import-'));
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/keys/importKeys.ts',
      'src/features/keys/importExample.ts',
      '--outDir',
      temporary,
      '--module',
      'commonjs',
      '--target',
      'es2020',
      '--skipLibCheck',
      '--strict',
    ],
    { cwd: root, stdio: 'pipe' },
  );
  fs.symlinkSync(path.join(root, 'node_modules'), path.join(temporary, 'node_modules'), 'dir');
} catch (error) {
  fs.rmSync(temporary, { recursive: true, force: true });
  throw error;
}
after(() => fs.rmSync(temporary, { recursive: true, force: true }));
const { parseAPIKeyImportFile } = require(path.join(temporary, 'importKeys.js'));
const { API_KEY_IMPORT_EXAMPLE } = require(path.join(temporary, 'importExample.js'));
function file(name, content) {
  const bytes = Buffer.from(content);
  return {
    name,
    size: bytes.length,
    text: async () => bytes.toString('utf8'),
    arrayBuffer: async () =>
      bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
  };
}

test('the downloadable CSV is a safe skeleton and works after complete credentials are supplied', async () => {
  await assert.rejects(
    parseAPIKeyImportFile(file('example.csv', API_KEY_IMPORT_EXAMPLE)),
    /No API keys/,
  );
  const filled = API_KEY_IMPORT_EXAMPLE.replace(
    'Production,',
    'Production,production-fixture-secret',
  ).replace('Staging,', 'Staging,staging-fixture-secret');
  assert.deepEqual(await parseAPIKeyImportFile(file('example.csv', '\uFEFF' + filled)), [
    { name: 'Production', api_key: 'production-fixture-secret' },
    { name: 'Staging', api_key: 'staging-fixture-secret' },
  ]);
});

test('documented CSV columns support quoted names and blank optional names', async () => {
  assert.deepEqual(
    await parseAPIKeyImportFile(
      file(
        'keys.csv',
        'name,api_key,ignored\n"Production, east",full-fixture-key-one,ignored\n,full-fixture-key-two,ignored',
      ),
    ),
    [
      { name: 'Production, east', api_key: 'full-fixture-key-one' },
      { name: '', api_key: 'full-fixture-key-two' },
    ],
  );
});

test('XLS and XLSX read only the first worksheet with the documented headers', async () => {
  const XLSX = require(path.join(root, 'node_modules', 'xlsx'));
  for (const [extension, bookType] of [
    ['xlsx', 'xlsx'],
    ['xls', 'biff8'],
  ]) {
    const workbook = XLSX.utils.book_new();
    XLSX.utils.book_append_sheet(
      workbook,
      XLSX.utils.aoa_to_sheet([
        ['name', 'api_key'],
        ['First', 'first-sheet-fixture-secret'],
      ]),
      'First',
    );
    XLSX.utils.book_append_sheet(
      workbook,
      XLSX.utils.aoa_to_sheet([
        ['name', 'api_key'],
        ['Ignored', 'second-sheet-fixture-secret'],
      ]),
      'Second',
    );
    const bytes = XLSX.write(workbook, { type: 'buffer', bookType });
    assert.deepEqual(await parseAPIKeyImportFile(file(`keys.${extension}`, bytes)), [
      { name: 'First', api_key: 'first-sheet-fixture-secret' },
    ]);
  }
});

test('file type, size and batch limits match the guidance', async () => {
  await assert.rejects(
    parseAPIKeyImportFile(file('keys.txt', 'fixture-secret')),
    /CSV, XLS, or XLSX/,
  );
  await assert.rejects(
    parseAPIKeyImportFile({ ...file('keys.csv', ''), size: 10 * 1024 * 1024 + 1 }),
    /10 MB/,
  );
  const rows = Array.from({ length: 500 }, (_, i) => `Key ${i},fixture-secret-${i}`);
  assert.equal(
    (await parseAPIKeyImportFile(file('keys.csv', 'name,api_key\n' + rows.join('\n')))).length,
    500,
  );
  await assert.rejects(
    parseAPIKeyImportFile(
      file('keys.csv', 'name,api_key\n' + [...rows, 'Extra,extra-fixture-secret'].join('\n')),
    ),
    /500 API keys/,
  );
});
