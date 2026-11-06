// Measure the static dependency graph for an authenticated Chinese overview.
// This measures compressed assets, not browser rendering time or request latency.
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const ts = require('typescript');
function measure(root) {
  const assets = path.join(root, 'assets');
  const files = fs.readdirSync(assets);
  const html = fs.readFileSync(path.join(root, 'index.html'), 'utf8');
  const main = html.match(/type="module"[^>]+src="\/assets\/([^"]+)"/)[1];
  const find = prefix => files.find(name => name.startsWith(prefix + '-') && name.endsWith('.js'));
  const closure = roots => {
    const seen = new Set();
    const visit = name => {
      if (!name || seen.has(name)) return;
      seen.add(name);
      const source = fs.readFileSync(path.join(assets, name), 'utf8');
      const tree = ts.createSourceFile(
        name,
        source,
        ts.ScriptTarget.Latest,
        true,
        ts.ScriptKind.JS,
      );
      for (const node of tree.statements) {
        if (!(ts.isImportDeclaration(node) || ts.isExportDeclaration(node))) continue;
        const dep = node.moduleSpecifier;
        if (dep && ts.isStringLiteral(dep) && dep.text.startsWith('.') && dep.text.endsWith('.js'))
          visit(path.basename(dep.text));
      }
    };
    roots.forEach(visit);
    return seen;
  };
  const initial = closure([main, find('Layout'), find('HomePage'), find('zh-CN')]);
  const gzip = name => zlib.gzipSync(fs.readFileSync(path.join(assets, name)), { level: 9 }).length;
  const total = names => Array.from(names).reduce((n, name) => n + gzip(name), 0);
  const extra = prefix => {
    const name = find(prefix);
    if (!name) return null;
    return {
      name,
      gzip: gzip(name),
      incrementalGzip: total(Array.from(closure([name])).filter(item => !initial.has(item))),
      inInitial: initial.has(name),
    };
  };
  const css = files.filter(name => /^(index|Layout|HomePage)-.*\.css$/.test(name));
  return {
    root,
    jsCount: initial.size,
    jsGzip: total(initial),
    cssGzip: total(css),
    initialGzip: total(initial) + total(css),
    docs: extra('docsMetadata'),
    verify: extra('verify-call'),
    files: Array.from(initial),
  };
}
const roots = process.argv.slice(2);
if (!roots.length || roots.length > 2) {
  console.error('Usage: node scripts/measure-console-bundle.cjs [baseline-dist] current-dist');
  process.exit(1);
}
const current = measure(path.resolve(roots.at(-1)));
const baseline = roots.length === 2 ? measure(path.resolve(roots[0])) : undefined;
console.log(
  JSON.stringify(
    {
      ...(baseline ? { baseline, delta: current.initialGzip - baseline.initialGzip } : {}),
      current,
    },
    null,
    2,
  ),
);
if (current.docs?.inInitial || current.verify?.inInitial) {
  console.error(
    'Deferred search metadata or call verification is in the initial console dependency graph.',
  );
  process.exitCode = 1;
}
