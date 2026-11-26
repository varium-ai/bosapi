export interface KeyTemplateSettings {
  organization_id: number | undefined;
  allowed_provider_ids: number[];
  allowed_models: string[];
  expires_at: string | null;
  quota_amount: number | null;
  quota_tokens: number | null;
  quota_reset_start_at?: string | null;
  daily_quota_amount: number | null;
  weekly_quota_amount: number | null;
  monthly_quota_amount: number | null;
  daily_quota_tokens: number | null;
  weekly_quota_tokens: number | null;
  monthly_quota_tokens: number | null;
  ip_allowlist: string[];
  ip_blocklist: string[];
}

export interface KeyTemplate {
  id: number;
  name: string;
  settings: KeyTemplateSettings;
}

export function emptyKeyTemplateSettings(organizationID?: number): KeyTemplateSettings {
  return {
    organization_id: organizationID,
    allowed_provider_ids: [],
    allowed_models: [],
    expires_at: null,
    quota_amount: null,
    quota_tokens: null,
    daily_quota_amount: null,
    weekly_quota_amount: null,
    monthly_quota_amount: null,
    daily_quota_tokens: null,
    weekly_quota_tokens: null,
    monthly_quota_tokens: null,
    ip_allowlist: [],
    ip_blocklist: [],
  };
}

export function getKeyTemplateSettings(values: Partial<KeyTemplateSettings>): KeyTemplateSettings {
  const settings = emptyKeyTemplateSettings(values.organization_id);
  for (const field of Object.keys(settings) as (keyof KeyTemplateSettings)[]) {
    if (values[field] !== undefined) Object.assign(settings, { [field]: values[field] });
  }
  settings.ip_allowlist = [...(settings.ip_allowlist || [])];
  settings.allowed_provider_ids = [...(settings.allowed_provider_ids || [])];
  settings.allowed_models = [...(settings.allowed_models || [])];
  settings.ip_blocklist = [...(settings.ip_blocklist || [])];
  return settings;
}
