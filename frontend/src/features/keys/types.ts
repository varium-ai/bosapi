import type { KeyTemplateSettings } from './templateSettings';
import type { UsageRecord } from './usagePresentation';

export interface APIKeyRecord extends Partial<KeyTemplateSettings>, UsageRecord {
  id: number;
  name: string;
  status: string;
  organization_id: number;
  security_inspection_upstream_enabled?: boolean;
  security_inspection_downstream_enabled?: boolean;
  org_id?: number;
  key_prefix?: string;
  key_suffix?: string;
  masked_key?: string;
}

export interface APIKeyFormValues extends Partial<KeyTemplateSettings> {
  name: string;
  status?: string;
  api_key?: string;
  security_inspection_upstream_enabled?: boolean;
  security_inspection_downstream_enabled?: boolean;
  quota_5h?: number | null;
  quota_1d?: number | null;
  quota_7d?: number | null;
  quota_5h_amount?: number | null;
  quota_1d_amount?: number | null;
  quota_7d_amount?: number | null;
  rate_limit_qps?: number;
}
