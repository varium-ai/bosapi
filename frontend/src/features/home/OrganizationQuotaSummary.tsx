import { Alert, Card, Progress, Skeleton } from 'antd';
import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { useI18n } from '../../i18n';
import { RefreshButton } from '../common/RefreshButton';

import {
  PERIODS,
  formatMillionTokens,
  quotaProgressPercent,
  rateLimitQueryOptions,
  type QuotaUser,
} from '../ratelimits/organizationQuota';

const PeriodRow: React.FC<{
  label: string;
  used: number | undefined;
  limit: number | null | undefined;
  formatUsed: (value: number) => string;
  formatLimit: (value: number) => string;
  blockedText: string;
  unlimitedText: string;
  unavailableText: string;
}> = ({
  label,
  used,
  limit,
  formatUsed,
  formatLimit,
  blockedText,
  unlimitedText,
  unavailableText,
}) => {
  const percent = quotaProgressPercent(used, limit);
  return (
    <div className="org-quota-period">
      <div className="org-quota-period-heading">
        <span>{label}</span>
        <span className="org-quota-period-value">
          {`${used == null ? unavailableText : formatUsed(used)} / ${
            limit == null ? unlimitedText : limit === 0 ? blockedText : formatLimit(limit)
          }`}
        </span>
      </div>
      {limit != null && used != null && (
        <Progress
          className="org-quota-progress"
          percent={percent}
          status={limit === 0 || percent >= 100 ? 'exception' : 'normal'}
          size="small"
          strokeColor={percent >= 100 ? '#dc2626' : percent >= 80 ? '#d97706' : '#2563eb'}
        />
      )}
    </div>
  );
};

export const OrganizationQuotaSummary: React.FC<{ user: QuotaUser }> = ({ user }) => {
  const { t, language, formatCurrency } = useI18n();
  const organizationId = user.organization_id;
  const query = useQuery({
    ...rateLimitQueryOptions(user, organizationId),
    enabled: user.role === 'org_admin' && organizationId > 0,
    refetchInterval: 30_000,
  });

  if (user.role !== 'org_admin' || !organizationId) return null;

  const organization = query.data?.organization;
  const usage = organization?.usage;
  const quotas = organization?.quotas;

  const rows = PERIODS.map(period => ({
    period,
    label: t(`keys.period_badge_${period}`, period),
    tokens: usage?.[`${period}_tokens` as const],
    tokenLimit: quotas?.[`${period}_tokens` as const] ?? null,
    amount: usage?.[`${period}_amount` as const],
    amountLimit: quotas?.[`${period}_amount` as const] ?? null,
  }));

  return (
    <Card
      className="org-quota-summary"
      title={t('overview.organization_quota_title', 'Organization Quota Summary')}
      extra={<RefreshButton loading={query.isFetching} onClick={() => void query.refetch()} />}
    >
      {query.isError && (
        <Alert
          type="warning"
          showIcon
          message={t('overview.organization_quota_failed', 'Quota summary unavailable')}
        />
      )}
      {query.isLoading ? (
        <Skeleton active paragraph={{ rows: 4 }} />
      ) : !query.isError && organization ? (
        rows.map(row => (
          <div key={row.period} className="org-quota-metrics">
            <PeriodRow
              label={`${row.label} · ${t('overview.tokens')}`}
              used={row.tokens}
              limit={row.tokenLimit}
              formatUsed={value => formatMillionTokens(value, language)}
              formatLimit={value => formatMillionTokens(value, language)}
              blockedText={t('ratelimits.org_blocked')}
              unlimitedText={t('keys.unlimited', 'No limit')}
              unavailableText={t('ratelimits.org_usage_unavailable')}
            />
            <PeriodRow
              label={`${row.label} · ${t('overview.cost')}`}
              used={row.amount}
              limit={row.amountLimit}
              formatUsed={value => formatCurrency(value, { decimals: 8 })}
              formatLimit={value => formatCurrency(value, { decimals: 8 })}
              blockedText={t('ratelimits.org_blocked')}
              unlimitedText={t('keys.unlimited', 'No limit')}
              unavailableText={t('ratelimits.org_usage_unavailable')}
            />
          </div>
        ))
      ) : null}
    </Card>
  );
};

export default OrganizationQuotaSummary;
