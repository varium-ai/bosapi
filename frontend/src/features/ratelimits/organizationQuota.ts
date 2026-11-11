import { queryOptions } from '@tanstack/react-query';
import client from '../../api/client';

export interface QuotaUser {
  id: number;
  role: string;
  organization_id: number;
}
export const PERIODS = ['daily', 'weekly', 'monthly'] as const;
export type Period = (typeof PERIODS)[number];
export const TOKENS_PER_MILLION = 1_000_000;
type Metric = 'tokens' | 'amount';
type Quotas = Partial<Record<`${Period}_${Metric}`, number | null>>;
export interface OrganizationQuota {
  organization_id: number;
  qps: number;
  quotas: Quotas;
  resets?: Partial<Record<Period, string>>;
  usage?: Partial<Record<`${Period}_${Metric}`, number>>;
}
export interface RateLimitSnapshot {
  degraded: boolean;
  runtime_state: string;
  global?: { qps: number; current_qps: number | null; remaining: number | null };
  organization?: OrganizationQuota | null;
  key?: {
    key_id: number;
    qps: number;
    quota_5h?: number | null;
    quota_1d?: number | null;
    quota_7d?: number | null;
  } | null;
}
export type OrganizationQuotaValues = {
  organization_id: number;
  qps: number;
} & Partial<Record<`${Period}_quota_${Metric}`, number | null>>;

export function formatMillionTokens(tokens: number, locale: string) {
  return `${new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(tokens / TOKENS_PER_MILLION)}M`;
}

export function millionTokenInputValue(tokens: number | null | undefined) {
  return tokens == null ? tokens : tokens / TOKENS_PER_MILLION;
}

export function tokensFromMillionInput(millions: number | null) {
  if (millions == null) return null;
  const tokens = Math.round(millions * TOKENS_PER_MILLION);
  return Number.isSafeInteger(tokens) ? tokens : Number.NaN;
}

export function quotaProgressPercent(used: number | undefined, limit: number | null | undefined) {
  if (limit === 0) return 100;
  if (limit == null || limit < 0 || used == null) return 0;
  return Math.round(Math.min(100, (used / limit) * 100) * 100) / 100;
}

export function quotaScope(user: QuotaUser) {
  return [user.id, user.role, user.organization_id] as const;
}
export function positiveId(value: string | null) {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}
export function resolveQuotaSelection(user: QuotaUser, params: URLSearchParams, firstOrg?: number) {
  if (user.role !== 'super_admin') {
    return { organizationId: user.organization_id || undefined, keyId: undefined };
  }
  return {
    organizationId:
      positiveId(params.get('organization_id')) || firstOrg || user.organization_id || undefined,
    keyId: positiveId(params.get('key_id')),
  };
}
export function rateLimitQueryOptions(user: QuotaUser, organizationId?: number, keyId?: number) {
  const allowed =
    user.role === 'super_admin' ||
    (user.role === 'org_admin' &&
      !!organizationId &&
      organizationId === user.organization_id &&
      !keyId);
  return queryOptions({
    queryKey: ['organization-rate-limits', ...quotaScope(user), organizationId, keyId],
    enabled: allowed,
    queryFn: async ({ signal }) => {
      if (!allowed) throw new Error('Invalid quota scope');
      const response = await client.get('/admin/rate-limits', {
        params: { organization_id: organizationId, key_id: keyId },
        signal,
        skipGlobalErrorMessage: true,
      });
      const data: RateLimitSnapshot | undefined = response.data?.data;
      if (
        !data ||
        typeof data.degraded !== 'boolean' ||
        typeof data.runtime_state !== 'string' ||
        (user.role === 'super_admin' && !Number.isFinite(data.global?.qps)) ||
        (organizationId &&
          (data.organization?.organization_id !== organizationId ||
            !data.organization.quotas ||
            !Number.isFinite(data.organization.qps))) ||
        (keyId && data.key?.key_id !== keyId)
      ) {
        throw new Error('Invalid rate limit snapshot');
      }
      return data;
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
}
export function organizationQuotaValues(organization: OrganizationQuota): OrganizationQuotaValues {
  const values: OrganizationQuotaValues = {
    organization_id: organization.organization_id,
    qps: organization.qps,
  };
  for (const period of PERIODS) {
    values[`${period}_quota_tokens`] = organization.quotas[`${period}_tokens`] ?? null;
    values[`${period}_quota_amount`] = organization.quotas[`${period}_amount`] ?? null;
  }
  return values;
}
export function validQuota(value: unknown, tokens: boolean) {
  return (
    value == null ||
    (typeof value === 'number' &&
      Number.isFinite(value) &&
      value >= 0 &&
      (!tokens || Number.isSafeInteger(value)))
  );
}
export function organizationQuotaUpdate(values: OrganizationQuotaValues) {
  if (
    !Number.isSafeInteger(values.organization_id) ||
    values.organization_id <= 0 ||
    !Number.isInteger(values.qps) ||
    values.qps < 0 ||
    values.qps > 1_000_000
  ) {
    throw new Error('Invalid organization or QPS');
  }
  const organization: OrganizationQuotaValues = {
    organization_id: values.organization_id,
    qps: values.qps,
  };
  for (const period of PERIODS) {
    for (const metric of ['tokens', 'amount'] as const) {
      const field = `${period}_quota_${metric}` as const;
      const value = values[field] ?? null;
      if (!validQuota(value, metric === 'tokens')) throw new Error('Invalid quota');
      organization[field] = value;
    }
  }
  return { organizations: [organization] };
}

// Fixed UTC calendar; independent of the browser's timezone and DST.
export function quotaResetAt(period: Period, now = Date.now()) {
  const current = new Date(now);
  const year = current.getUTCFullYear();
  const month = current.getUTCMonth();
  const day = current.getUTCDate();
  const next =
    period === 'monthly'
      ? Date.UTC(year, month + 1, 1)
      : Date.UTC(year, month, day + (period === 'weekly' ? 7 - ((current.getUTCDay() + 6) % 7) : 1));
  return new Date(next).toISOString();
}
