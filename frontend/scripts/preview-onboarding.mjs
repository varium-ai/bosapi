// In-memory fixtures for the local preview only. Never imported by the app.
export function createOnboardingPreview() {
  let scenario;
  let organizations = [];
  let providers = [];
  let keys = [];
  let bindings = new Map();
  let sequence = 10;
  const reset = (next, role) => {
    scenario = next;
    organizations =
      next === 'empty' && role === 'admin'
        ? []
        : [
            {
              id: 1,
              name: 'Demo company',
              description: 'First Key setup preview',
              status: 'active',
            },
          ];
    providers = ['ready', 'complete', 'models'].includes(next)
      ? [
          {
            id: 1,
            name: 'Demo public channel',
            platform: 'openai',
            account_type: 'apikey',
            api_format: 'openai_chat',
            base_url: 'https://example.invalid/v1',
            status: 'active',
            schedulable: true,
            is_private: false,
            updated_at: '2026-01-01T00:00:00Z',
            verified_at: next === 'models' ? null : '2026-01-02T00:00:00Z',
            models: next === 'models' ? [] : ['demo-model'],
          },
        ]
      : [];
    keys =
      next === 'complete'
        ? [{ id: 1, name: 'Existing Key', organization_id: 1, status: 'active', prefix: 'sk-demo' }]
        : [];
    bindings = new Map(['ready', 'complete'].includes(next) ? [[1, [1]]] : []);
    sequence = 10;
    if (next === 'ready') {
      organizations.push({ id: 2, name: 'Another initialized company', status: 'active' });
      keys.push({
        id: 2,
        name: 'Other organization Key',
        organization_id: 2,
        status: 'active',
        prefix: 'sk-other',
      });
    }
  };
  const handle = async (req, res, url) => {
    if (!scenario) return false;
    const path = url.pathname.replace('/api/v1', '');
    if (!path.startsWith('/admin/')) return false;
    const role = req.headers.authorization === 'Bearer ui-preview-org' ? 'org' : 'admin';
    const respond = data => {
      res.end(JSON.stringify({ data }));
      return true;
    };
    const list = items => {
      const page = Number(url.searchParams.get('page') || 1);
      const size = Number(url.searchParams.get('page_size') || 20);
      return respond({ items: items.slice((page - 1) * size, page * size), total: items.length });
    };
    const body = async () => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      return JSON.parse(Buffer.concat(chunks).toString() || '{}');
    };
    const organizationView = org => ({
      ...org,
      has_api_keys: keys.some(key => key.organization_id === org.id),
      has_provider_bindings: (bindings.get(org.id) || []).length > 0,
      has_active_bindings: (bindings.get(org.id) || []).length > 0,
      allowed_provider_ids: bindings.get(org.id) || [],
      allowed_models: [
        ...new Set(
          providers
            .filter(provider => (bindings.get(org.id) || []).includes(provider.id))
            .flatMap(provider => provider.models),
        ),
      ],
    });
    if (path === '/admin/organizations' && req.method === 'GET') {
      if (scenario === 'error') {
        res.statusCode = 500;
        res.end(JSON.stringify({ error: { message: 'Preview: setup status unavailable' } }));
        return true;
      }
      return list(
        organizations.filter(org => role === 'admin' || org.id === 1).map(organizationView),
      );
    }
    if (path === '/admin/organizations' && req.method === 'POST') {
      const values = await body();
      const org = { ...values, id: ++sequence };
      organizations.push(org);
      bindings.set(org.id, values.allowed_provider_ids || []);
      return respond(organizationView(org));
    }
    const orgDetail = path.match(/^\/admin\/organizations\/(\d+)$/);
    if (orgDetail) {
      const org = organizations.find(item => item.id === Number(orgDetail[1]));
      if (req.method === 'PUT') {
        const values = await body();
        Object.assign(org, values);
        bindings.set(org.id, values.allowed_provider_ids || []);
      }
      return respond(organizationView(org));
    }
    if (/^\/admin\/organizations\/\d+\/provider-bindings$/.test(path)) {
      const id = Number(path.split('/')[3]);
      return respond({
        items: (bindings.get(id) || []).map(account_id => ({
          account_id,
          status: 'active',
          priority: 0,
          model_allowlist: [],
        })),
      });
    }
    if (path === '/admin/providers' && req.method === 'GET') {
      const orgID = role === 'org' ? 1 : Number(url.searchParams.get('organization_id'));
      const visible = providers.filter(
        provider =>
          !orgID ||
          (provider.is_private
            ? provider.owner_organization_id === orgID
            : (bindings.get(orgID) || []).includes(provider.id)),
      );
      return list(visible);
    }
    const oauthComplete = path.match(
      /^\/admin\/providers\/oauth\/(openai|anthropic|gemini|antigravity)\/complete$/,
    );
    if (req.method === 'POST' && (path === '/admin/providers' || oauthComplete)) {
      const input = await body();
      const values = oauthComplete ? input.provider : input;
      const provider = {
        ...values,
        api_key: undefined,
        id: ++sequence,
        status: 'active',
        schedulable: true,
        is_private: role === 'org',
        owner_organization_id: role === 'org' ? 1 : undefined,
      };
      providers.push(provider);
      return respond(provider);
    }
    const modelDiscovery = path.match(/^\/admin\/providers\/(\d+)\/discover-models$/);
    if (modelDiscovery && req.method === 'POST') {
      const provider = providers.find(item => item.id === Number(modelDiscovery[1]));
      if (provider && provider.platform !== 'gemini') {
        provider.models = ['demo-subscription-model'];
      }
      return respond({
        provider_id: provider?.id,
        items: (provider?.models || []).map(model_name => ({ model_name, status: 'active' })),
      });
    }
    const providerDetail = path.match(/^\/admin\/providers\/(\d+)$/);
    if (providerDetail) {
      const provider = providers.find(item => item.id === Number(providerDetail[1]));
      if (req.method === 'PUT') Object.assign(provider, await body(), { api_key: undefined });
      return respond(provider);
    }
    const modelList = path.match(/^\/admin\/providers\/(\d+)\/models$/);
    if (modelList) {
      const provider = providers.find(item => item.id === Number(modelList[1]));
      return list(
        (provider?.models || []).map((model_name, index) => ({
          id: index + 1,
          model_name,
          status: 'active',
        })),
      );
    }
    const setupStatus = path.match(/^\/admin\/organizations\/(\d+)\/setup-status$/);
    if (setupStatus) {
      const id = Number(setupStatus[1]);
      const provider = providers.find(
        item =>
          item.models?.length &&
          (item.is_private
            ? item.owner_organization_id === id
            : (bindings.get(id) || []).includes(item.id)),
      );
      return respond({
        has_usable_key: keys.some(key => key.organization_id === id && key.status === 'active'),
        successful_call_at: scenario === 'complete' ? new Date().toISOString() : null,
        provider_id: provider?.id || 0,
        provider_updated_at: provider?.updated_at || null,
        channel_verified: !!provider?.verified_at,
        can_test: role === 'admin' || !!provider?.is_private,
      });
    }
    const testChannel = path.match(/^\/admin\/providers\/(\d+)\/test$/);
    if (testChannel && req.method === 'POST') {
      const provider = providers.find(item => item.id === Number(testChannel[1]));
      if (provider) provider.verified_at = new Date().toISOString();
      return respond({ success: true, latency_ms: 12 });
    }
    if (path === '/admin/keys' && req.method === 'GET') {
      const orgID = role === 'org' ? 1 : Number(url.searchParams.get('organization_id'));
      return list(
        keys
          .filter(key => !orgID || key.organization_id === orgID)
          .map(key => ({
            ...key,
            organization_name: organizations.find(org => org.id === key.organization_id)?.name,
          })),
      );
    }
    if (path === '/admin/keys' && req.method === 'POST') {
      const values = await body();
      const key = {
        ...values,
        id: ++sequence,
        status: 'active',
        prefix: 'sk-preview',
        created_at: new Date().toISOString(),
      };
      keys.push(key);
      return respond({ ...key, key: 'sk-preview-only-not-a-real-credential' });
    }
    if (
      [
        '/admin/usage/total',
        '/admin/usage/daily',
        '/admin/usage/weekly',
        '/admin/usage/monthly',
      ].includes(path)
    )
      return respond({ items: [] });
    if (
      [
        '/admin/usage',
        '/admin/usage/organizations/daily',
        '/admin/request-logs',
        '/admin/security-events',
        '/admin/model-metadata',
        '/admin/key-templates',
      ].includes(path)
    )
      return list([]);
    if (path === '/admin/status')
      return respond({
        version: 'local-preview',
        sampled_at: new Date().toISOString(),
        sample_interval_seconds: 30,
        collection_ms: 1,
        uptime_seconds: 300,
        draining: false,
        mysql: { status: 'ok', latency_ms: 1 },
        redis: { status: 'not_configured' },
        queue: { state: 'running', depth: 0 },
        nodes: [],
        certificates: [],
        history: [],
      });
    return false;
  };
  return { reset, handle };
}
