#!/usr/bin/env node
// Keep developer explanations in English without corrupting product translations
// or Unicode and security-detection fixtures.
const fs = require('node:fs');
const path = require('node:path');
const ts = require('../frontend/node_modules/typescript');
const root = path.resolve(__dirname, '..');
const ignored = new Set(['.git', 'node_modules', 'dist', 'coverage', '__pycache__', 'third_party']);
const failures = new Set();
let checked = 0;
function record(file, source, start, comment) {
  if (/\p{Script=Han}/u.test(comment)) {
    const line = source.slice(0, start).split('\n').length;
    failures.add(`${file}:${line}: use English for developer comments and documentation`);
  }
}
function comments(source) {
  const found = [];
  let i = 0;
  while (i < source.length) {
    const current = source[i];
    if (current === '"' || current === "'" || current === '`') {
      const quote = current;
      i++;
      while (i < source.length) {
        if (quote !== '`' && source[i] === '\\') { i += 2; continue; }
        if (source[i++] === quote) break;
      }
    } else if (source.startsWith('//', i)) {
      const start = i;
      while (i < source.length && source[i] !== '\n') i++;
      found.push([start, source.slice(start, i)]);
    } else if (source.startsWith('/*', i)) {
      const start = i;
      const end = source.indexOf('*/', i + 2);
      i = end < 0 ? source.length : end + 2;
      found.push([start, source.slice(start, i)]);
    } else i++;
  }
  return found;
}
function visit(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (ignored.has(entry.name)) continue;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) { visit(absolute); continue; }
    if (!entry.isFile()) continue;
    const file = path.relative(root, absolute).split(path.sep).join('/');
    const source = fs.readFileSync(absolute, 'utf8');
    if (file.endsWith('.md')) {
      if (!/\.(zh|ja|de|es)\.md$/.test(file)) record(file, source, 0, source);
    } else if (/\.(?:ts|tsx|js|jsx|cjs|mjs)$/.test(file)) {
      const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
      const ranges = new Map();
      const collect = node => {
        for (const position of [node.pos, node.end]) {
          for (const range of [...(ts.getLeadingCommentRanges(source, position) || []), ...(ts.getTrailingCommentRanges(source, position) || [])])
            ranges.set(range.pos, range.end);
        }
        ts.forEachChild(node, collect);
      };
      collect(tree);
      for (const [start, end] of ranges) record(file, source, start, source.slice(start, end));
    } else if (file.endsWith('.go')) {
      for (const [start, comment] of comments(source)) record(file, source, start, comment);
    } else if (/\.(?:sh|py|yml|yaml)$/.test(file)) {
      let offset = 0;
      for (const line of source.split('\n')) {
        if (/^\s*#/.test(line)) record(file, source, offset, line);
        offset += line.length + 1;
      }
    }
    checked++;
  }
}
visit(root);
if (failures.size) {
  for (const failure of failures) console.error(failure);
  process.exitCode = 1;
} else console.log(`English documentation/comment check passed (${checked} files; product localization and Unicode fixtures preserved).`);
