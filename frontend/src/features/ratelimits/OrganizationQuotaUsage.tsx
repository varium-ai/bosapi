import { Table, Typography } from 'antd';
import { useI18n } from '../../i18n';
import { formatDateTime } from '../../i18n/formatters';
import {
  PERIODS,
  formatMillionTokens,
  quotaResetAt,
  type OrganizationQuota,
} from './organizationQuota';

export function OrganizationQuotaUsage({
  organization,
  millionTokens = false,
}: {
  organization: OrganizationQuota;
  millionTokens?: boolean;
}) {
  const { t, language, formatTokens, formatCurrency } = useI18n();
  const formatTokenValue = (value: number) =>
    millionTokens ? formatMillionTokens(value, language) : formatTokens(value);
  const limit = (value: number | null | undefined, amount: boolean) =>
    value == null
      ? t('keys.unlimited')
      : value === 0
        ? t('ratelimits.org_blocked')
        : amount
          ? formatCurrency(value, { decimals: 8 })
          : formatTokenValue(value);
  const used = (value: number | undefined, amount: boolean) =>
    value == null
      ? t('ratelimits.org_usage_unavailable')
      : amount
        ? formatCurrency(value, { decimals: 8 })
        : formatTokenValue(value);
  return (
    <>
      <Table
        size="small"
        rowKey="period"
        pagination={false}
        scroll={{ x: 640 }}
        dataSource={PERIODS.map(period => ({
          period,
          label: t(`ratelimits.org_${period}_usage`),
          tokens: `${used(organization.usage?.[`${period}_tokens`], false)} / ${limit(organization.quotas[`${period}_tokens`], false)}`,
          amount: `${used(organization.usage?.[`${period}_amount`], true)} / ${limit(organization.quotas[`${period}_amount`], true)}`,
          reset: formatDateTime(
            organization.resets?.[period] ?? quotaResetAt(period),
            'UTC',
            language,
          ),
        }))}
        columns={[
          { title: t('keys.quota_hover_period'), dataIndex: 'label' },
          { title: t('ratelimits.org_tokens_usage_limit'), dataIndex: 'tokens' },
          { title: t('ratelimits.org_amount_usage_limit'), dataIndex: 'amount' },
          { title: t('ratelimits.org_reset_at'), dataIndex: 'reset' },
        ]}
      />
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {t('ratelimits.org_calendar_hint')}
      </Typography.Paragraph>
    </>
  );
}
