import { brotliCompress, constants, gzip } from 'node:zlib';
import { promisify } from 'node:util';
import { readdir, readFile, unlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { Plugin, ResolvedConfig } from 'vite';
import type { OutputBundle, OutputChunk } from 'rollup';

const gzipAsync = promisify(gzip);
const brotliCompressAsync = promisify(brotliCompress);

const ROUTE_MODULES: Record<string, string> = {
  '/': '/features/home/HomePage.tsx',
  '/admin/organizations': '/features/organizations/OrganizationsPage.tsx',
  '/admin/keys': '/features/keys/KeysPage.tsx',
  '/admin/providers': '/features/providers/ProvidersPage.tsx',
  '/admin/usage': '/features/usage/UsagePage.tsx',
  '/admin/requests': '/features/usage/UsagePage.tsx',
  '/admin/errors': '/features/errors/ErrorsPage.tsx',
  '/admin/security-events': '/features/security/SecurityEventsPage.tsx',
  '/admin/status': '/features/status/StatusPage.tsx',
  '/admin/rate-limits': '/features/ratelimits/RateLimitsPage.tsx',
  '/admin/admin-users': '/features/adminusers/AdminUsersPage.tsx',
  '/admin/message-config': '/features/adminusers/MessageNotificationConfigPage.tsx',
  '/admin/request-logs': '/features/requestlogs/RequestLogsPage.tsx',
};

function isChunk(item: OutputBundle[string]): item is OutputChunk {
  return item.type === 'chunk';
}

function findChunk(bundle: OutputBundle, moduleSuffix: string) {
  return Object.values(bundle).find(
    (item): item is OutputChunk =>
      isChunk(item) && item.facadeModuleId?.replaceAll('\\', '/').endsWith(moduleSuffix) === true,
  );
}

function directFile(chunk: OutputChunk | undefined) {
  return chunk ? [chunk.fileName] : [];
}

function preloadBootstrap(bundle: OutputBundle) {
  const layout = directFile(findChunk(bundle, '/app/Layout.tsx'));
  const routes = Object.fromEntries(
    Object.entries(ROUTE_MODULES).flatMap(([route, moduleSuffix]) => {
      const chunk = findChunk(bundle, moduleSuffix);
      return chunk ? [[route, directFile(chunk)]] : [];
    }),
  );
  const keyDetail = directFile(findChunk(bundle, '/features/keys/KeyDetailPage.tsx'));
  const keyPortal = directFile(findChunk(bundle, '/features/keys/KeyPortalPage.tsx'));
  const login = directFile(findChunk(bundle, '/features/auth/LoginPage.tsx'));
  const docs = directFile(findChunk(bundle, '/features/docs/DocsPage.tsx'));
  const locales = {
    'zh-CN': [
      ...directFile(findChunk(bundle, '/i18n/locales/zh-CN.ts')),
      ...directFile(findChunk(bundle, '/antd/es/locale/zh_CN.js')),
    ],
    'en-US': [
      ...directFile(findChunk(bundle, '/i18n/locales/en-US.ts')),
      ...directFile(findChunk(bundle, '/antd/es/locale/en_US.js')),
    ],
  };

  return `<script>(function(){var p=location.pathname,a=[],r=${JSON.stringify(routes)},l=${JSON.stringify(layout)},d=${JSON.stringify(docs)},g=${JSON.stringify(login)},k=${JSON.stringify(keyDetail)},q=${JSON.stringify(keyPortal)},m=${JSON.stringify(locales)};var z='zh-CN';try{var s=localStorage.getItem('bos_lang');if(s==='en-US')z=s}catch(e){}a=a.concat(m[z]||[]);var x=false,y=false;try{x=!!localStorage.getItem('auth_token');y=!!localStorage.getItem('key_portal_token')}catch(e){}if(p==='/login')a=a.concat(g);else if(p==='/docs'||p.indexOf('/docs/')===0)a=a.concat(d);else if(p.indexOf('/key-portal')===0&&y)a=a.concat(l,q);else if(x){var h=p.indexOf('/admin/keys/')===0&&p.split('/').length===4?k:r[p];a=a.concat(l,h||[])}Array.from(new Set(a)).forEach(function(f){var e=document.createElement('link');e.rel='modulepreload';e.href='/'+f;e.crossOrigin='anonymous';document.head.appendChild(e)})})();</script>`;
}

async function filesBelow(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map(async entry => {
      const target = path.join(directory, entry.name);
      // Vite also copies Finder/Explorer metadata from public into the build.
      // Remove only known metadata files; publication still validates all others.
      if (
        entry.isFile() &&
        (entry.name === '.DS_Store' || entry.name === 'Thumbs.db' || entry.name.startsWith('._'))
      ) {
        await unlink(target);
        return [];
      }
      return entry.isDirectory() ? filesBelow(target) : [target];
    }),
  );
  return nested.flat();
}

export function coldStartPlugin(): Plugin {
  let config: ResolvedConfig;
  return {
    name: 'bosapi-cold-start',
    enforce: 'post',
    configResolved(resolved) {
      config = resolved;
    },
    generateBundle(_options, bundle) {
      const html = bundle['index.html'];
      if (!html || html.type !== 'asset' || typeof html.source !== 'string') return;
      html.source = html.source.replace('</head>', `${preloadBootstrap(bundle)}</head>`);
    },
    async closeBundle() {
      if (config.command !== 'build') return;
      const outputDirectory = path.resolve(config.root, config.build.outDir);
      const candidates = (await filesBelow(outputDirectory)).filter(file =>
        /\.(?:css|html|js|json|svg)$/.test(file),
      );
      await Promise.all(
        candidates.map(async file => {
          const source = await readFile(file);
          if (source.length < 256) return;
          const [gzipped, brotli] = await Promise.all([
            gzipAsync(source, { level: 9 }),
            brotliCompressAsync(source, {
              params: {
                [constants.BROTLI_PARAM_QUALITY]: 11,
                [constants.BROTLI_PARAM_MODE]: constants.BROTLI_MODE_TEXT,
              },
            }),
          ]);
          await Promise.all([writeFile(`${file}.gz`, gzipped), writeFile(`${file}.br`, brotli)]);
        }),
      );
    },
  };
}
