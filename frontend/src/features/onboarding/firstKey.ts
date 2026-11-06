export interface SetupUser {
  id: number;
  role: string;
  organization_id?: number;
}

export interface SetupOrganization {
  id: number;
  name: string;
  status: string;
  has_api_keys: boolean;
  has_provider_bindings?: boolean;
  allowed_models?: string[];
}

export interface SetupProvider {
  id: number;
  name: string;
  status: string;
  schedulable?: boolean;
  api_format?: string;
  verified_at?: string | null;
  updated_at?: string;
}

export async function collectSetupPages<T>(
  fetchPage: (page: number) => Promise<{ items: T[]; total: number }>,
) {
  const items: T[] = [];
  for (let page = 1; ; page++) {
    const data = await fetchPage(page);
    if (!Array.isArray(data?.items) || !Number.isSafeInteger(data.total) || data.total < 0)
      throw new Error('Invalid setup response');
    items.push(...data.items);
    if (items.length >= data.total) return items;
    if (!data.items.length) throw new Error('Incomplete setup response');
  }
}

export function positiveID(value: string | null) {
  if (!value || !/^[1-9]\d*$/.test(value)) return undefined;
  const id = Number(value);
  return Number.isSafeInteger(id) ? id : undefined;
}

export function setupOrganizations(items: SetupOrganization[], user: SetupUser) {
  if (user.role === 'super_admin') return items.filter(item => item.status === 'active');
  if (user.role === 'org_admin')
    return items.filter(item => item.id === user.organization_id && item.status === 'active');
  return [];
}

export function selectSetupOrganization(items: SetupOrganization[], requested?: number) {
  return items.find(item => item.id === requested) || items.find(item => !item.has_api_keys);
}

export function setupProgress(input: {
  organizationExists: boolean;
  channelConfigured: boolean;
  channelVerified: boolean;
  organizationReady: boolean;
  hasUsableKey: boolean;
  successfulCallAt?: string | null;
  providerUpdatedAt?: string;
}) {
  const order = ['channel', 'organization', 'key', 'call'];
  const successful =
    !!input.successfulCallAt &&
    input.organizationReady &&
    input.channelConfigured &&
    input.channelVerified &&
    (!input.providerUpdatedAt ||
      Date.parse(input.successfulCallAt) >= Date.parse(input.providerUpdatedAt));
  const completed: Record<string, boolean> = {
    channel: input.channelConfigured && input.channelVerified,
    organization: input.organizationExists && input.organizationReady,
    key: input.hasUsableKey,
    call: successful && input.hasUsableKey,
  };
  return {
    order,
    completed,
    successful: completed.call,
    current: order.find(name => !completed[name]),
    count: order.filter(name => completed[name]).length,
  };
}

export function firstKeyPath(path: string, organizationID?: number, action?: string) {
  const params = new URLSearchParams({ onboarding: 'first-key' });
  if (organizationID) params.set('setup_org', String(organizationID));
  if (action) params.set('setup_action', action);
  if (path === '/admin/keys' && organizationID)
    params.set('organization_id', String(organizationID));
  return `${path}?${params}`;
}

// Channels must expose a model allowed by the organization's binding policy.
export function hasSetupModel(
  provider: SetupProvider,
  models: { model_name: string; status?: string }[],
  organization?: SetupOrganization,
) {
  if (provider.status !== 'active' || provider.schedulable === false) return false;
  return models.some(model => {
    if (
      !model.model_name ||
      model.model_name === '*' ||
      (model.status && model.status !== 'active')
    )
      return false;
    if (!organization || !organization.has_provider_bindings) return true;
    const normalize = (name: string) =>
      provider.api_format === 'gemini' ? name.replace(/^models\//, '') : name;
    return (organization.allowed_models || []).some(
      name => normalize(name) === normalize(model.model_name),
    );
  });
}

export const setupActions = [
  {
    action: 'create-provider',
    path: '/admin/providers',
    title: ['Configure a channel', '配置上游渠道'],
    keywords: ['供应商', '上游', '渠道', 'provider', 'upstream'],
    superAdminOnly: true,
  },
  {
    action: 'create-organization',
    path: '/admin/organizations',
    title: ['Create an organization', '创建组织'],
    keywords: ['组织', '分配模型', 'organization'],
    superAdminOnly: true,
  },
  {
    action: 'create-key',
    path: '/admin/keys',
    title: ['Create API Key', '创建 API Key'],
    keywords: ['创建 key', '密钥', '令牌', 'token', 'create key'],
    superAdminOnly: false,
  },
] as const;

export function parseSetupPreferences(raw: string | null): Record<string, boolean> {
  try {
    const value = JSON.parse(raw || '{}');
    if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
    return Object.fromEntries(
      Object.entries(value).filter(
        ([key, enabled]) => (key === 'instance' || positiveID(key)) && typeof enabled === 'boolean',
      ),
    ) as Record<string, boolean>;
  } catch {
    return {};
  }
}

export interface SetupEvidence {
  has_usable_key: boolean;
  successful_call_at: string | null;
  provider_id: number;
  provider_updated_at: string | null;
  channel_verified: boolean;
  can_test: boolean;
}

export function parseSetupEvidence(value: any): SetupEvidence {
  const timestamp = (date: unknown) =>
    date === null || (typeof date === 'string' && Number.isFinite(Date.parse(date)));
  if (
    !value ||
    typeof value.has_usable_key !== 'boolean' ||
    !timestamp(value.successful_call_at) ||
    !Number.isSafeInteger(value.provider_id) ||
    value.provider_id < 0 ||
    !timestamp(value.provider_updated_at) ||
    typeof value.channel_verified !== 'boolean' ||
    typeof value.can_test !== 'boolean'
  )
    throw new Error('Incomplete setup evidence');
  return value;
}
