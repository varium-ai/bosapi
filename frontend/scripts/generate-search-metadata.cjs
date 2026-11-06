const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const cache = new Map();
function load(file) {
  file = path.resolve(file);
  if (cache.has(file)) return cache.get(file);
  const result = { exports: {} };
  cache.set(file, result.exports);
  const source = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  new Function('exports', 'require', 'module', source)(
    result.exports,
    name =>
      name.startsWith('.') ? load(path.resolve(path.dirname(file), name + '.ts')) : require(name),
    result,
  );
  return result.exports;
}
const root = path.resolve(__dirname, '..');
const en = load(path.join(root, 'src/features/docs/content.ts')).docPages;
const zh = en;
const entries = en.flatMap(page => {
  const translated = zh.find(item => item.slug === page.slug);
  return [
    {
      href: `/docs/${page.slug}`,
      name: [page.title, translated?.title || page.title],
      description: [page.description, translated?.description || page.description],
    },
    ...page.sections.map(section => ({
      href: `/docs/${page.slug}#${section.id}`,
      name: [
        section.title,
        translated?.sections.find(item => item.id === section.id)?.title || section.title,
      ],
      description: [page.title, translated?.title || page.title],
    })),
  ];
});
const output = require('prettier').format(JSON.stringify(entries), {
  ...JSON.parse(fs.readFileSync(path.join(root, '.prettierrc.json'), 'utf8')),
  parser: 'json',
});
const file = path.join(root, 'src/features/search/docsMetadata.json');
Promise.resolve(output).then(output => {
  if (process.argv.includes('--check')) {
    if (fs.readFileSync(file, 'utf8') !== output)
      throw new Error(
        'Documentation search metadata is stale; run node scripts/generate-search-metadata.cjs',
      );
  } else fs.writeFileSync(file, output);
});
