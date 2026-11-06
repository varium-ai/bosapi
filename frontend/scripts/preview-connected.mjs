import { createServer } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const target = process.env.VITE_BACKEND_URL || 'https://bosapi.typex-test.cn';
const proxy = { target, changeOrigin: true, secure: true };
const server = await createServer({
  root,
  configFile: false,
  envFile: false,
  define: {
    'import.meta.env.VITE_API_BASE_URL': JSON.stringify('/api/v1'),
  },
  plugins: [
    react(),
    {
      name: 'connected-backend-preview',
      transformIndexHtml() {
        return [
          {
            tag: 'div',
            attrs: {
              style:
                'position:fixed;bottom:12px;left:12px;z-index:999;padding:6px 10px;border:1px solid #e5e5e5;border-radius:6px;background:#fff;color:#737373;font:11px system-ui;pointer-events:none',
            },
            children: 'LOCAL FRONTEND / CONNECTED BACKEND',
            injectTo: 'body',
          },
        ];
      },
    },
  ],
  server: {
    host: '127.0.0.1',
    port: 5180,
    strictPort: true,
    proxy: { '/api': proxy, '/v1': proxy, '/health': proxy, '/images': proxy },
  },
});

await server.listen();
console.log(`Local frontend: http://127.0.0.1:5180/; real backend: ${target}`);
console.log('No API fixtures. Sign in using an existing test account.');
