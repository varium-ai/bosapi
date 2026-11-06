import { Table, Typography } from 'antd';
import { useI18n } from '../../i18n';

type Period = 'daily' | 'weekly' | 'monthly';
type QuotaField =
  | `${Period}_quota_tokens`
  | `${Period}_quota_amount`
  | `${Period}_used_tokens`
  | `${Period}_used_amount`
  | 'quota_tokens'
  | 'quota_amount'
  | 'quota_used_tokens'
  | 'quota_used_amount';
export type KeyQuotaData = Partial<Record<QuotaField, number | string | null>> & {
  usage_stats_status?: string;
};

export function KeyQuotaTable({
  data,
  loading = false,
}: {
  data: KeyQuotaData;
  loading?: boolean;
}) {
  const { t, formatCurrency, formatTokens } = useI18n();
  const statisticsReady =
    !data.usage_stats_status ||
    ['ready', 'refreshing', 'refresh_failed'].includes(data.usage_stats_status);
  const rows = (['daily', 'weekly', 'monthly', 'total'] as const).map(period => {
    const available = period === 'total' || statisticsReady;
    const tokenLimit =
      data[period === 'total' ? 'quota_tokens' : (`${period}_quota_tokens` as const)];
    const amountLimit =
      data[period === 'total' ? 'quota_amount' : (`${period}_quota_amount` as const)];
    return {
      period,
      label: t(`keys.period_badge_${period}`),
      hasLimit: tokenLimit != null || amountLimit != null,
      tokenLimit: tokenLimit != null ? formatTokens(tokenLimit) : t('keys.unlimited'),
      tokenUsed: available
        ? formatTokens(
            data[period === 'total' ? 'quota_used_tokens' : (`${period}_used_tokens` as const)] ??
              0,
          )
        : '-',
      amountLimit: amountLimit != null ? formatCurrency(amountLimit) : t('keys.unlimited'),
      amountUsed: available
        ? formatCurrency(
            data[period === 'total' ? 'quota_used_amount' : (`${period}_used_amount` as const)] ??
              0,
            { decimals: 4 },
          )
        : '-',
    };
  });
  return (
    <Table
      rowKey="period"
      size="small"
      pagination={false}
      loading={loading}
      scroll={{ x: 720 }}
      dataSource={rows}
      columns={[
        {
          title: t('keys.quota_hover_period'),
          dataIndex: 'label',
          width: 160,
          render: (label, row) => (
            <div className="console-quota-period">
              <Typography.Text strong={row.hasLimit}>{label}</Typography.Text>
              {row.hasLimit && <span className="console-chip">{t('keys.limits_configured')}</span>}
            </div>
          ),
        },
        { title: t('keys.limit_tokens'), dataIndex: 'tokenLimit', align: 'right', width: 140 },
        { title: t('keys.detail_tokens'), dataIndex: 'tokenUsed', align: 'right', width: 140 },
        { title: t('keys.limit_amount'), dataIndex: 'amountLimit', align: 'right', width: 140 },
        { title: t('keys.detail_cost'), dataIndex: 'amountUsed', align: 'right', width: 140 },
      ]}
    />
  );
}
