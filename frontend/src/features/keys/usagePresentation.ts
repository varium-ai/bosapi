export type UsageRecord = Record<string, unknown>;
export const quotaPeriods = ['total', 'daily', 'weekly', 'monthly'] as const;
export type QuotaPeriod = (typeof quotaPeriods)[number];
export interface QuotaSummary {
  period: QuotaPeriod;
  unit: 'amount' | 'tokens';
  used: number | null;
  limit: number;
  ratio: number | null;
}

export function usageNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

export function quotaFields(period: QuotaPeriod, unit: 'amount' | 'tokens') {
  return period === 'total'
    ? { used: `quota_used_${unit}`, limit: `quota_${unit}` }
    : { used: `${period}_used_${unit}`, limit: `${period}_quota_${unit}` };
}

// Prefer monetary quotas, falling back to tokens; all limits remain in the popover.
export function primaryQuota(record: UsageRecord): QuotaSummary | null {
  const quotas: QuotaSummary[] = [];
  for (const unit of ['amount', 'tokens'] as const) {
    for (const period of quotaPeriods) {
      const fields = quotaFields(period, unit);
      const limit = usageNumber(record[fields.limit]);
      if (limit === null) continue;
      const used = usageNumber(record[fields.used]);
      quotas.push({
        period,
        unit,
        used,
        limit,
        ratio: used === null ? null : limit === 0 ? 1 : used / limit,
      });
    }
  }
  const amounts = quotas.filter(quota => quota.unit === 'amount');
  return (
    (amounts.length ? amounts : quotas).sort((a, b) => (b.ratio ?? -1) - (a.ratio ?? -1))[0] ?? null
  );
}

export function displayUsageRecord(record: UsageRecord): UsageRecord {
  if (
    !record.usage_stats_status ||
    ['ready', 'refreshing', 'refresh_failed'].includes(String(record.usage_stats_status))
  )
    return record;
  const result = { ...record };
  for (const period of ['daily', 'weekly', 'monthly'])
    for (const unit of ['amount', 'tokens']) result[`${period}_used_${unit}`] = null;
  return result;
}
