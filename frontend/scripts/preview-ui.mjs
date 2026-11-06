import { createServer } from 'vite';
import { randomUUID } from 'node:crypto';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { createOnboardingPreview } from './preview-onboarding.mjs';
import { createAnnouncementPreview } from './preview-announcements.mjs';

// Standalone, loopback-only UI fixtures. Never loaded by the application build.
const root = fileURLToPath(new URL('../', import.meta.url));
const port = Number(process.env.UI_PREVIEW_PORT || 5180);
const onboardingPreview = createOnboardingPreview();
const announcementPreview = createAnnouncementPreview();
const mfaSetup = {
  secret: 'JBSWY3DPEHPK3PXP',
  otpauth_url: 'otpauth://totp/BOS%20API:demo?secret=JBSWY3DPEHPK3PXP&issuer=BOS%20API',
};
const profiles = {
  admin: {
    id: 1,
    email: 'admin@bosapi.local',
    role: 'super_admin',
    status: 'active',
    totp_enabled: true,
  },
  org: {
    id: 2,
    email: 'manager@bosapi.local',
    organization_id: 1,
    role: 'org_admin',
    status: 'active',
    totp_enabled: false,
  },
  key: { id: 3, email: 'demo-key', organization_id: 1, role: 'key_viewer', status: 'active' },
};
let gatewaySettings = {
  endpoint: '',
};
let messageSettings = {
  provider: 'lark',
  lark_webhook: '',
  webhook_url: '',
  smtp_host: '',
  smtp_port: 587,
  smtp_username: '',
  smtp_password: '',
  from_email: '',
  from_name: '',
  alert_emails: [],
};

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
      name: 'isolated-ui-preview',
      transformIndexHtml() {
        return [
          {
            tag: 'div',
            attrs: {
              style:
                'position:fixed;bottom:12px;left:12px;z-index:999;padding:6px 10px;border:1px solid #e5e5e5;border-radius:6px;background:#fff;color:#737373;font:11px system-ui;pointer-events:none',
            },
            children: 'LOCAL UI PREVIEW · Demo data only',
            injectTo: 'body',
          },
        ];
      },
      configureServer(vite) {
        vite.middlewares.use(async (req, res, next) => {
          const url = new URL(req.url, 'http://127.0.0.1');
          if (url.pathname === '/__preview') {
            const role = Object.hasOwn(profiles, url.searchParams.get('role'))
              ? url.searchParams.get('role')
              : 'admin';
            const scenario = url.searchParams.get('onboarding');
            if (['empty', 'ready', 'models', 'complete', 'error'].includes(scenario))
              onboardingPreview.reset(scenario, role);
            const startPath =
              url.searchParams.get('page') === 'api-address' ? '/admin/api-address' : '/';
            res.setHeader('Content-Type', 'text/html; charset=utf-8');
            res.setHeader('Cache-Control', 'no-store');
            res.end(`<!doctype html><title>Local UI preview</title><script>
            localStorage.removeItem('refresh_token');
            localStorage.removeItem('key_portal_token');
            localStorage.removeItem('key_portal_info');
            localStorage.setItem('auth_token', ${JSON.stringify(`ui-preview-${role}`)});
            localStorage.setItem('user_info', ${JSON.stringify(JSON.stringify(profiles[role]))});
            if (${JSON.stringify(role)} === 'key') localStorage.setItem('key_portal_token', 'ui-preview-key');
            location.replace(${JSON.stringify(startPath)});
          </script>`);
            return;
          }
          if (!/^\/(api|v1|health|images)(\/|$)/.test(url.pathname)) return next();
          res.setHeader('Content-Type', 'application/json');
          res.setHeader('Cache-Control', 'no-store');
          const respond = data => res.end(JSON.stringify({ data }));
          if (await announcementPreview(req, res, url)) return;
          if (url.pathname === '/api/v1/key-portal/info')
            return respond({
              id: 3,
              name: 'Demo API Key',
              organization_id: 1,
              status: 'active',
              key_prefix: 'sk-demo',
            });
          if (/^\/api\/v1\/key-portal\/usage\//.test(url.pathname)) return respond({ items: [] });
          if (await onboardingPreview.handle(req, res, url)) return;
          const oauthStart = url.pathname.match(
            /^\/api\/v1\/admin\/providers\/oauth\/(openai|anthropic|gemini|antigravity)\/start$/,
          );
          if (req.method === 'POST' && oauthStart) {
            // Show the link/callback form without connecting to any OAuth service.
            const state = randomUUID();
            return respond({
              session_id: randomUUID(),
              auth_url: `https://example.invalid/oauth/${oauthStart[1]}?state=${state}`,
              state,
              expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
            });
          }
          if (req.method === 'GET' && url.pathname === '/api/v1/platform/config') {
            return respond({
              gateway_endpoint: gatewaySettings.endpoint,
            });
          }
          if (url.pathname === '/api/v1/admin/settings/gateway-endpoints') {
            if (req.headers.authorization !== 'Bearer ui-preview-admin') {
              res.statusCode = 403;
              return res.end(
                JSON.stringify({ error: { message: 'Super administrator access is required.' } }),
              );
            }
            if (req.method === 'GET') return respond(gatewaySettings);
            if (req.method === 'PUT') {
              const chunks = [];
              for await (const chunk of req) chunks.push(chunk);
              const values = JSON.parse(Buffer.concat(chunks).toString());
              const endpoint = values.endpoint?.trim().replace(/\/+$/, '').replace(/\/v1$/, '');
              if (!endpoint || !/^https?:\/\/[^/]+/.test(endpoint)) {
                res.statusCode = 400;
                return res.end(JSON.stringify({ error: { message: 'Invalid API address' } }));
              }
              gatewaySettings = { endpoint };
              return respond(gatewaySettings);
            }
            if (req.method === 'DELETE') {
              gatewaySettings = { endpoint: '' };
              return respond(gatewaySettings);
            }
          }
          if (url.pathname === '/api/v1/admin/message-notification-settings') {
            if (req.headers.authorization !== 'Bearer ui-preview-admin') {
              res.statusCode = 403;
              return res.end(
                JSON.stringify({ error: { message: 'Super administrator access is required.' } }),
              );
            }
            if (req.method === 'GET') {
              const { lark_webhook, webhook_url, smtp_password, ...visible } = messageSettings;
              return respond({
                ...visible,
                has_lark_webhook: Boolean(lark_webhook),
                masked_lark_webhook: lark_webhook
                  ? 'https://open.feishu.cn/open-apis/bot/v2/hook/****'
                  : '',
                has_webhook_url: Boolean(webhook_url),
                masked_webhook_url: webhook_url ? `${new URL(webhook_url).origin}/****` : '',
                has_smtp_password: Boolean(smtp_password),
              });
            }
            if (req.method === 'PUT') {
              let values;
              try {
                const chunks = [];
                for await (const chunk of req) chunks.push(chunk);
                values = JSON.parse(Buffer.concat(chunks).toString());
              } catch {
                res.statusCode = 400;
                return res.end(JSON.stringify({ error: { message: 'Invalid preview request' } }));
              }
              messageSettings = {
                ...messageSettings,
                ...values,
                lark_webhook: values.lark_webhook || messageSettings.lark_webhook,
                webhook_url: values.webhook_url || messageSettings.webhook_url,
                smtp_password: values.smtp_password || messageSettings.smtp_password,
              };
              return respond({ status: 'saved' });
            }
          }
          if (
            req.method === 'POST' &&
            ['/api/v1/auth/login', '/api/v1/auth/mfa/verify'].includes(url.pathname)
          ) {
            let body;
            try {
              const chunks = [];
              for await (const chunk of req) chunks.push(chunk);
              body = JSON.parse(Buffer.concat(chunks).toString());
            } catch {
              res.statusCode = 400;
              return res.end(JSON.stringify({ error: { message: 'Invalid preview request' } }));
            }
            if (url.pathname.endsWith('/login')) {
              const setup = body.email !== profiles.admin.email;
              return respond({
                mfa_required: true,
                mfa_setup_required: setup,
                mfa_token: `ui-preview-mfa-${setup ? 'setup' : 'enabled'}`,
                ...(setup ? mfaSetup : {}),
              });
            }
            const role =
              body.mfa_token === 'ui-preview-mfa-setup'
                ? 'org'
                : body.mfa_token === 'ui-preview-mfa-enabled'
                  ? 'admin'
                  : null;
            if (!role || body.code !== '123456') {
              res.statusCode = 401;
              return res.end(
                JSON.stringify({
                  error: { message: 'Verification code is invalid (preview code: 123456)' },
                }),
              );
            }
            return respond({
              access_token: `ui-preview-${role}`,
              user: { ...profiles[role], totp_enabled: true },
            });
          }
          if (req.method === 'GET' && url.pathname === '/api/v1/auth/me') {
            const role = String(req.headers.authorization || '').replace('Bearer ui-preview-', '');
            return respond(profiles[role] || profiles.admin);
          }
          if (req.method === 'POST' && url.pathname === '/api/v1/auth/logout') return respond({});
          if (req.method === 'POST' && url.pathname === '/api/v1/auth/mfa/setup') {
            return respond({ ...mfaSetup, expires_in: 300 });
          }
          res.statusCode = 501;
          res.end(
            JSON.stringify({
              error: { message: 'UI preview only: this action is not connected to a backend.' },
            }),
          );
        });
      },
    },
  ],
  server: { host: '127.0.0.1', port, strictPort: true },
});

await server.listen();
console.log(`Local UI preview: http://127.0.0.1:${port}/__preview`);
console.log(
  'Fixtures: ?role=admin (default), ?role=org, ?role=key. Account mutations are disabled.',
);
console.log(
  'MFA login fixtures: manager@bosapi.local (setup), admin@bosapi.local (enabled), any dummy password; verification code: 123456.',
);
