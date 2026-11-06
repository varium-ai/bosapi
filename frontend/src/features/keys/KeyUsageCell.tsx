import React from 'react';
import { Button, Popover, Progress, Typography, theme } from 'antd';
import { InfoCircleOutlined } from '@ant-design/icons';
import { useI18n } from '../../i18n';
import {
  primaryQuota,
  displayUsageRecord,
  quotaFields,
  quotaPeriods,
  usageNumber,
  type UsageRecord,
} from './usagePresentation';
import './key-usage.css';

export const KeyUsageCell: React.FC<{ record: UsageRecord }> = ({ record: rawRecord }) => {
  const record = displayUsageRecord(rawRecord);
  const { t, formatCurrency, formatTokens, formatDateTime } = useI18n();
  const { token } = theme.useToken();
  const quota = primaryQuota(record);
  const money = (value: unknown, decimals = 4, minDecimals = decimals) => {
    const amount = usageNumber(value);
    return amount === null ? '—' : formatCurrency(amount, { decimals, minDecimals });
  };
  const todayAmount = money(
    record.usage_today_amount === undefined ? record.daily_used_amount : record.usage_today_amount,
  );
  const recentAmount = money(record.usage_30d_amount);
  const detail = (period: (typeof quotaPeriods)[number], unit: 'amount' | 'tokens') => {
    const fields = quotaFields(period, unit);
    const used = usageNumber(record[fields.used]);
    const limit = usageNumber(record[fields.limit]);
    const format = unit === 'amount' ? (value: number) => money(value, 4, 2) : formatTokens;
    return (
      <>
        {used === null ? '—' : format(used)}
        {limit !== null && (
          <span style={{ color: token.colorTextTertiary }}> / {format(limit)}</span>
        )}
      </>
    );
  };
  const content = (
    <div className="key-usage-popover">
      {!!record.usage_stats_as_of && (
        <Typography.Text type="secondary">
          {t('usage.snapshot_as_of')}: {formatDateTime(String(record.usage_stats_as_of))}
        </Typography.Text>
      )}
      {!!record.usage_stats_status &&
        !['ready', 'refreshing'].includes(String(record.usage_stats_status)) && (
          <Typography.Text type="warning">
            {t(
              `usage.snapshot_${record.usage_stats_status === 'refresh_failed' ? 'refresh_failed' : 'generating'}`,
            )}
          </Typography.Text>
        )}
      <div className="key-usage-popover-heading">
        <Typography.Text strong>{t('keys.quota_hover_title')}</Typography.Text>
        <Typography.Text type="secondary" className="key-usage-caption">
          {t('keys.usage_used_limit')}
        </Typography.Text>
      </div>
      <div className="key-usage-table-scroll">
        <table aria-label={t('keys.quota_hover_title')}>
          <thead style={{ color: token.colorTextSecondary, background: token.colorFillAlter }}>
            <tr>
              <th className="key-usage-period">{t('keys.quota_hover_period')}</th>
              <th>{t('keys.usage_amount')}</th>
              <th>Token</th>
            </tr>
          </thead>
          <tbody>
            {quotaPeriods.map(period => (
              <tr key={period} style={{ borderBottom: `1px solid ${token.colorBorderSecondary}` }}>
                <td className="key-usage-period" style={{ color: token.colorTextSecondary }}>
                  {t(`keys.period_badge_${period}`)}
                </td>
                <td>{detail(period, 'amount')}</td>
                <td>{detail(period, 'tokens')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="key-usage-period-note" style={{ color: token.colorTextTertiary }}>
        {t('keys.usage_period_note')}
      </div>
    </div>
  );
  return (
    <div className="key-usage-cell">
      <div className="key-usage-summary">
        <span className="key-usage-amount" title={`${t('keys.usage_today')}: ${todayAmount}`}>
          <Typography.Text type="secondary">{t('keys.usage_today')}: </Typography.Text>
          {todayAmount}
        </span>
        <span className="key-usage-amount" title={`${t('keys.usage_30d')}: ${recentAmount}`}>
          <Typography.Text type="secondary">{t('keys.usage_30d')}: </Typography.Text>
          {recentAmount}
        </span>
        <Popover
          content={content}
          trigger={['hover', 'focus']}
          placement="topRight"
          mouseEnterDelay={0.15}
          styles={{ body: { padding: 12, borderRadius: 10 } }}
        >
          <Button
            type="text"
            size="small"
            icon={<InfoCircleOutlined />}
            aria-label={t('keys.quota_hover_title')}
            className="key-usage-info"
            style={{ color: token.colorTextTertiary }}
          />
        </Popover>
      </div>
      {quota && (
        <div className="key-usage-quota">
          <div>
            <Typography.Text type="secondary">
              {t(`keys.usage_quota_${quota.period}`)}
              {quota.unit === 'tokens' ? ' (Token)' : ''}:{' '}
            </Typography.Text>
            {quota.unit === 'amount'
              ? money(quota.used, 4, 2)
              : quota.used === null
                ? '—'
                : formatTokens(quota.used)}{' '}
            / {quota.unit === 'amount' ? money(quota.limit, 2) : formatTokens(quota.limit)}
          </div>
          {quota.ratio !== null && (
            <Progress
              percent={Math.min(100, Math.max(0, quota.ratio * 100))}
              showInfo={false}
              size="small"
              status={quota.ratio >= 1 ? 'exception' : 'normal'}
              strokeColor={
                quota.ratio >= 1
                  ? undefined
                  : quota.ratio >= 0.8
                    ? 'var(--warning)'
                    : 'var(--success)'
              }
              className="key-usage-progress"
            />
          )}
        </div>
      )}
    </div>
  );
};
