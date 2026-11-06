import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import { coldStartPlugin } from './coldStartPlugin';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const backendTarget = env.VITE_BACKEND_URL || 'http://127.0.0.1:8081';
  const proxyEntry = { target: backendTarget, changeOrigin: true };

  return {
    plugins: [react(), coldStartPlugin()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      host: '127.0.0.1',
      port: 3000,
      proxy: {
        '/api': proxyEntry,
        '/v1': proxyEntry,
        '/images': proxyEntry,
        '/health': proxyEntry,
      },
    },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      rollupOptions: {
        output: {
          experimentalMinChunkSize: 15_000,
          onlyExplicitManualChunks: true,
          manualChunks(id) {
            // Keep the explicit, potentially billable test flow outside the initial console bundle.
            if (id.endsWith('/features/onboarding/VerifyCall.tsx')) return 'verify-call';
            if (id.endsWith('/features/search/docsMetadata.json')) return 'docsMetadata';
            if (!id.includes('/node_modules/')) return;
            if (
              /\/(react|react-dom|scheduler|react-router|react-router-dom|@remix-run\/router)\//.test(
                id,
              )
            )
              return 'vendor-react';
            if (id.includes('/@tanstack/')) return 'vendor-query';
            if (id.includes('/axios/')) return 'vendor-http';
            if (
              /\/(?:@ant-design\/(?:colors|cssinjs|fast-color)|classnames|rc-util|dayjs)\//.test(id)
            )
              return 'vendor-ui-core';
          },
        },
      },
    },
  };
});
