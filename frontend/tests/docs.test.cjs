const { test, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'bos-docs-test-'));
const root = path.resolve(__dirname, '..');
try {
  execFileSync(
    process.execPath,
    [
      require.resolve('typescript/bin/tsc'),
      'src/features/docs/content.ts',
      'src/features/docs/localize.ts',
      'src/features/docs/helpers.ts',
      'src/features/docs/brands.ts',
      'src/i18n/docsMessages.ts',
      '--rootDir',
      'src',
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
} catch (error) {
  fs.rmSync(temporary, { recursive: true, force: true });
  throw new Error(error.stdout?.toString() || error.message);
}
after(() => fs.rmSync(temporary, { recursive: true, force: true }));

const { docPages, docGroups } = require(path.join(temporary, 'features/docs/content.js'));
const { docsEnglish, docsChinese } = require(path.join(temporary, 'i18n/docsMessages.js'));
const { normalizeDocOrigin, fillSnippet, searchDocs, DEFAULT_CONNECTION } = require(
  path.join(temporary, 'features/docs/helpers.js'),
);
const { resolveGatewayConfig } = require(path.join(temporary, 'config/gatewayEndpoint.js'));
const allBlocks = docPages.flatMap(page => page.sections.flatMap(section => section.blocks));
const examples = allBlocks.flatMap(block =>
  block.type === 'code' ? [block] : block.type === 'tabs' ? block.tabs : [],
);

test('guide routes and section anchors are unique and stable', () => {
  assert.ok(docPages.length >= 18);
  assert.equal(new Set(docPages.map(page => page.slug)).size, docPages.length);
  for (const page of docPages) {
    assert.match(page.slug, /^[a-z0-9-]+$/);
    assert.ok(page.description && page.audience && page.sections.length >= 2, page.slug);
    assert.equal(
      new Set(page.sections.map(section => section.id)).size,
      page.sections.length,
      page.slug,
    );
    for (const section of page.sections) {
      assert.match(section.id, /^[a-z0-9-]+$/);
      assert.ok(section.title && section.blocks.length);
    }
  }
});

test('all in-content guide links resolve', () => {
  for (const block of allBlocks.filter(block => block.type === 'cards')) {
    for (const card of block.items) {
      const [slug, anchor] = card.href.replace('/docs/', '').split('#');
      const page = docPages.find(item => item.slug === slug);
      assert.ok(page, card.href);
      assert.ok(!anchor || page.sections.some(section => section.id === anchor), card.href);
    }
  }
});

test('scope is product use, console, API and coding agents, not deployment', () => {
  assert.deepEqual(docGroups, [
    'Getting started',
    'Using the console',
    'API reference',
    'Coding agents',
    'Help',
  ]);
  assert.ok(!docPages.some(page => /deployment|architecture/i.test(page.title)));
  for (const slug of ['codex', 'claude-code', 'cursor', 'cline-roo', 'opencode']) {
    const page = docPages.find(page => page.slug === slug);
    assert.ok(
      page?.sources?.every(source => source.url.startsWith('https://')),
      slug,
    );
  }
});

test('URLs normalize without duplicating v1', () => {
  assert.equal(normalizeDocOrigin(' https://gateway.example/v1/ '), 'https://gateway.example');
  assert.equal(
    normalizeDocOrigin('https://gateway.example/prefix/v1'),
    'https://gateway.example/prefix',
  );
  assert.equal(normalizeDocOrigin('http://localhost:8081/'), 'http://localhost:8081');
  assert.equal(normalizeDocOrigin('http://[::1]:8081'), 'http://[::1]:8081');
  assert.equal(normalizeDocOrigin('http://gateway.example.com/v1/'), 'http://gateway.example.com');
});

test('reject credential-bearing or executable example URLs', () => {
  for (const value of [
    'javascript:alert(1)',
    'ftp://gateway.example',
    'https://user:pass@gateway.example',
    'https://gateway.example?key=secret',
    'https://gateway.example/#fragment',
    'https://gateway.example/$(whoami)',
    'https://gateway.example/"quoted"',
    'not a url',
  ]) {
    assert.throws(() => normalizeDocOrigin(value), undefined, value);
  }
});

test('every template resolves and uses secret placeholders or environment variables', () => {
  for (const example of examples) {
    const rendered = fillSnippet(example.code, DEFAULT_CONNECTION);
    assert.ok(!/\{\{\w+\}\}/.test(rendered), example.label);
    assert.ok(!/^\+\s+-H/m.test(rendered), example.label);
    assert.ok(!/\/v1\/v1/.test(rendered), example.label);
    assert.ok(!/key_prefix|sk-gw-[a-zA-Z0-9]{12}/.test(rendered));
    if (example.language === 'json') assert.doesNotThrow(() => JSON.parse(rendered));
  }
});

test('shell examples have valid syntax without executing requests', () => {
  for (const example of examples.filter(example => example.language === 'bash')) {
    execFileSync('bash', ['-n'], {
      input: fillSnippet(example.code, DEFAULT_CONNECTION),
      stdio: 'pipe',
    });
  }
});

test('search finds titles, body text and error codes with correct anchors', () => {
  assert.equal(searchDocs(docPages, 'Codex CLI')[0].page.slug, 'codex');
  assert.equal(searchDocs(docPages, 'model_not_allowed')[0].page.slug, 'troubleshooting');
  assert.equal(searchDocs(docPages, 'model_not_allowed')[0].section.id, 'errors');
  assert.ok(
    searchDocs(docPages, 'periodic token').some(result => result.page.slug === 'routing-limits'),
  );
  assert.deepEqual(searchDocs(docPages, '   '), []);
  assert.deepEqual(searchDocs(docPages, 'not-a-real-search-term-738492'), []);
});

test('examples follow explicit connection changes across all languages', () => {
  const connection = { origin: 'https://test.example/proxy', model: 'org/custom-model' };
  for (const example of examples) {
    const rendered = fillSnippet(example.code, connection);
    if (example.code.includes('{{baseUrl}}'))
      assert.ok(rendered.includes('https://test.example/proxy/v1'));
    if (example.code.includes('{{model}}')) assert.ok(rendered.includes('org/custom-model'));
  }
});

test('documentation UI translations are complete with matching placeholders', () => {
  assert.deepEqual(Object.keys(docsEnglish).sort(), Object.keys(docsChinese).sort());
  for (const key of Object.keys(docsEnglish)) {
    assert.ok(docsChinese[key], key);
    assert.deepEqual(docsEnglish[key].match(/\{\w+\}/g), docsChinese[key].match(/\{\w+\}/g), key);
  }
});

test('documentation is standalone with an account-menu entry, not a sidebar entry', () => {
  const routes = fs.readFileSync(path.join(root, 'src/app/routes.tsx'), 'utf8');
  assert.ok(routes.indexOf('path="/docs/:slug"') < routes.indexOf('<ProtectedRoute>'));
  const layout = fs.readFileSync(path.join(root, 'src/app/Layout.tsx'), 'utf8');
  assert.ok(
    !layout.slice(0, layout.indexOf('const getPageTitle')).includes("key: '/docs/quickstart'"),
  );
  const accountMenu = fs.readFileSync(path.join(root, 'src/app/AccountMenu.tsx'), 'utf8');
  assert.match(layout, /<AccountMenu\b/);
  assert.match(
    accountMenu,
    /key: 'documentation',[\s\S]*?<a[^>]*href="\/docs\/quickstart"[^>]*target="_blank"[^>]*rel="noopener noreferrer"/,
  );
});

test('console header has one account entry without duplicate security or locale actions', () => {
  const layout = fs.readFileSync(path.join(root, 'src/app/Layout.tsx'), 'utf8');
  const header = layout.slice(layout.indexOf('<Header'), layout.indexOf('</Header>'));
  assert.equal((header.match(/<AccountMenu\b/g) || []).length, 1);
  assert.doesNotMatch(header, /<Button\b|<Dropdown\b|<LocaleSettingsControl\b/);
  assert.match(header, /onMFA=\{handleOpenMFA\}/);
  assert.match(header, /onChangePassword=/);
  assert.match(header, /onLogout=\{handleLogout\}/);
});

test('documentation console entry uses the console label and dashboard icon', () => {
  assert.equal(docsChinese.console, '\u63a7\u5236\u53f0');
  assert.equal(docsEnglish.console, 'Console');
  const source = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  const entry = source.match(/<Link className="docs-console-link"[\s\S]*?<\/Link>/)?.[0];
  assert.ok(entry);
  assert.match(entry, /to="\/"/);
  assert.match(entry, /<DashboardOutlined aria-hidden="true" \/>/);
  assert.doesNotMatch(entry, /ExportOutlined/);
});

test('only the address saved for this deployment is used for API access', () => {
  const options = {
    runtimeEndpoint: 'https://runtime.example/v1/',
    buildEndpoint: 'https://build.example',
    publicEndpoint: 'https://public.example',
    origin: 'http://127.0.0.1:5173',
  };
  assert.deepEqual(resolveGatewayConfig(options), {
    endpoint: 'https://public.example',
    baseUrl: 'https://public.example/v1',
  });
  delete options.publicEndpoint;
  assert.deepEqual(resolveGatewayConfig(options), {
    endpoint: '',
    baseUrl: '',
  });
  assert.deepEqual(
    resolveGatewayConfig({
      ...options,
      prod_endpoint: 'https://production.example',
      test_endpoint: 'https://staging.example',
    }),
    { endpoint: '', baseUrl: '' },
  );
  assert.deepEqual(resolveGatewayConfig({ publicEndpoint: '' }), { endpoint: '', baseUrl: '' });
});

test('invalid gateway overrides cannot leak credentials into examples', () => {
  const result = resolveGatewayConfig({
    runtimeEndpoint: 'https://user:password@bad.example',
    buildEndpoint: 'javascript:alert(1)',
    publicEndpoint: 'https://bad.example/?key=secret',
    origin: 'https://current.example',
  });
  assert.deepEqual(result, {
    endpoint: '',
    baseUrl: '',
  });
});

test('unconfigured documentation uses explicit placeholders instead of relative request URLs', () => {
  assert.equal(
    fillSnippet('{{origin}} {{baseUrl}}/models', { origin: '', model: 'YOUR_MODEL_ID' }),
    'YOUR_API_ADDRESS YOUR_API_ADDRESS/v1/models',
  );
});

test('customer UI contains no environment chooser or bundled alternate gateway domains', () => {
  for (const file of [
    'src/config/gateway.ts',
    'src/config/gatewayEndpoint.ts',
    'src/features/settings/GatewaySettingsPage.tsx',
    'src/features/home/HomePage.tsx',
    'src/features/docs/DocsPage.tsx',
  ]) {
    const source = fs.readFileSync(path.join(root, file), 'utf8');
    assert.doesNotMatch(
      source,
      /prodEndpoint|testEndpoint|prod_endpoint|test_endpoint|active_environment|bosapi-test\.net|bosapi\.net|typex-test\.cn|DEFAULT_GATEWAY_ENDPOINTS|restore_defaults|selectedEnv/,
    );
  }
  const docs = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  assert.match(docs, /origin: gateway.endpoint/);
});

test('documentation never fetches or presents live model inventory', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  assert.doesNotMatch(source, /ModelCatalog|PublicModel|catalogState|datalist|fetch\(/);
  const home = fs.readFileSync(path.join(root, 'src/features/home/HomePage.tsx'), 'utf8');
  assert.doesNotMatch(home, /public-models|modelsData|modelColumns/);
  for (const pages of [docPages]) {
    assert.ok(
      !pages.some(page =>
        page.sections.some(section => section.blocks.some(block => block.type === 'models')),
      ),
    );
    const guide = pages.find(page => page.slug === 'models');
    assert.match(JSON.stringify(guide), /YOUR_MODEL_ID/);
    assert.doesNotMatch(JSON.stringify(guide), /Qwen3|DeepSeek-V4|GLM-5/);
  }
});

test('sidebar is not duplicated and input focus uses one control-level ring', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  assert.doesNotMatch(source, /docs-sidebar-label|ExperimentOutlined|DeploymentUnitOutlined/);
  const css = fs.readFileSync(path.join(root, 'src/features/docs/docs.css'), 'utf8');
  assert.match(css, /\.docs-search:focus-within\s*\{[^}]*box-shadow:/);
  assert.match(css, /\.docs-search input:focus-visible\s*\{[^}]*outline: none/);
});

test('documentation keeps reading controls without redundant article chrome', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  const css = fs.readFileSync(path.join(root, 'src/features/docs/docs.css'), 'utf8');
  const removed =
    /docs-breadcrumb|docs-article-meta|docs-sidebar-footer|docs-version|docs-toc-help|docs-bottom-note|docs-footer/;
  assert.doesNotMatch(source, removed);
  assert.doesNotMatch(css, removed);
  assert.doesNotMatch(source, /className="docs-eyebrow">\{page.group\}/);
  for (const control of [
    'docs-search',
    'docs-locale',
    'docs-mobile-toc',
    'docs-toc',
    'docs-pagination',
    'docs-sources',
  ]) {
    assert.ok(source.includes(`className="${control}"`), control);
  }
});

test('documentation has no example configuration form or instructions to use it', () => {
  const source = fs.readFileSync(path.join(root, 'src/features/docs/DocsPage.tsx'), 'utf8');
  assert.doesNotMatch(source, /ConnectionSettings|<form|setModel|docs-connection/);
  assert.match(source, /\.\.\.DEFAULT_CONNECTION, origin: gateway.endpoint/);
  for (const pages of [docPages]) {
    assert.ok(
      pages.every(page =>
        page.sections.every(section => section.blocks.every(block => block.type !== 'connection')),
      ),
    );
    assert.doesNotMatch(
      JSON.stringify(pages),
      /Update examples|\u66f4\u65b0\u793a\u4f8b|example settings|\u6587\u6863\u8f93\u5165\u6846/,
    );
    assert.match(JSON.stringify(pages.find(page => page.slug === 'claude-code')), /YOUR_MODEL_ID/);
  }
});

test('coding agents use local official brand assets with recorded provenance', () => {
  const { createHash } = require('node:crypto');
  const { agentBrands } = require(path.join(temporary, 'features/docs/brands.js'));
  const provenance = JSON.parse(
    fs.readFileSync(path.join(root, 'public/docs/brands/sources.json'), 'utf8'),
  );
  for (const page of docPages.filter(
    page => page.group === 'Coding agents' && page.slug !== 'coding-agents',
  )) {
    assert.ok(agentBrands[page.icon]?.length, page.slug);
    for (const brand of agentBrands[page.icon]) {
      const source = provenance.assets.find(asset => asset.file === brand.file);
      assert.ok(source?.url.startsWith('https://'), brand.file);
      const bytes = fs.readFileSync(path.join(root, 'public/docs/brands', brand.file));
      assert.equal(createHash('sha256').update(bytes).digest('hex'), source.sha256);
      if (brand.file.endsWith('.svg')) {
        assert.match(bytes.toString(), /<svg/);
        assert.doesNotMatch(bytes.toString(), /<script|<foreignObject|\bon\w+\s*=/i);
      } else assert.equal(bytes.subarray(1, 4).toString(), 'PNG');
    }
  }
  assert.deepEqual(
    agentBrands['cline-roo'].map(brand => brand.name),
    ['Cline', 'Roo Code'],
  );
});

test('selected client coverage excludes removed tools', () => {
  const required = [
    'codex-desktop',
    'claude-code',
    'claude-desktop',
    'codex',
    'cline-roo',
    'openclaw',
    'opencode',
    'cursor',
  ];
  for (const pages of [docPages]) {
    for (const slug of required) {
      const page = pages.find(page => page.slug === slug);
      assert.ok(page?.sources?.length, slug);
      assert.ok(
        page.sections.some(section => section.id === 'requirements'),
        slug,
      );
      for (const id of ['install', 'configure', 'verify'])
        assert.ok(
          page.sections.some(section => section.id === id),
          `${slug}#${id}`,
        );
    }
    const clients = pages.filter(
      page =>
        page.group === 'Coding agents' || page.group === '\u7f16\u7a0b\u52a9\u624b\u63a5\u5165',
    );
    assert.doesNotMatch(
      JSON.stringify(clients),
      /deepseek-harness|WorkBuddy|Trae|Cherry Studio|CC Switch|cc-switch|Gemini CLI|gemini-cli|Aider/i,
    );
    assert.match(
      JSON.stringify(pages.find(page => page.slug === 'claude-desktop')),
      /Configure Third-Party Inference/,
    );
  }
});

test('desktop and terminal instructions have distinct credential lifetimes', () => {
  const desktop = JSON.stringify(docPages.find(page => page.slug === 'codex-desktop'));
  assert.match(desktop, /launchctl setenv BOS_GATEWAY_KEY/);
  assert.match(desktop, /SetEnvironmentVariable/);
  assert.match(desktop, /current login session/);
  assert.match(desktop, /config\.toml\.txt/);
  for (const slug of ['codex', 'claude-code', 'opencode']) {
    const tabs = docPages
      .find(page => page.slug === slug)
      .sections.flatMap(section => section.blocks)
      .filter(block => block.type === 'tabs');
    assert.ok(
      tabs.some(block =>
        block.tabs.some(tab => tab.language === 'powershell' && tab.code.includes('$env:')),
      ),
      slug,
    );
  }
});

test('inline guide links resolve and no competitor configuration leaks into examples', () => {
  for (const pages of [docPages]) {
    const text = JSON.stringify(pages);
    for (const match of text.matchAll(/\]\(\/docs\/([a-z0-9-]+)(?:#([a-z0-9-]+))?\)/g)) {
      const target = pages.find(page => page.slug === match[1]);
      assert.ok(target, match[0]);
      assert.ok(!match[2] || target.sections.some(section => section.id === match[2]), match[0]);
    }
    assert.doesNotMatch(text, /teamorouter|sk-teamo-|rm -rf|dangerously-skip-permissions/i);
  }
});

test('product-facing copy uses BOS API without a gateway tagline', () => {
  assert.equal(docPages.find(page => page.slug === 'quickstart').title, 'Get started with BOS API');
  for (const pages of [docPages])
    assert.doesNotMatch(JSON.stringify(pages), /LLM \u5b89\u5168\u7f51\u5173|security gateway/i);
  for (const file of [
    'src/app/Layout.tsx',
    'src/i18n/locales/en-US.ts',
    'src/i18n/locales/zh-CN.ts',
  ]) {
    assert.doesNotMatch(
      fs.readFileSync(path.join(root, file), 'utf8'),
      /LLM \u5b89\u5168\u7f51\u5173|LLM Security Gateway/i,
    );
  }
});
