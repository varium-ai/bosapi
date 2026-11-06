import type { ReactNode } from 'react';
import { Statistic, Tooltip } from 'antd';
import { useI18n } from '../../i18n';
import { formatUsageCount } from './tokenRanking';

export function UsageCountStatistic({ title, value }: { title: ReactNode; value: number }) {
  const { language, formatNumber } = useI18n();
  return (
    <Statistic
      title={title}
      value={value}
      formatter={count => (
        <Tooltip title={formatNumber(Number(count))} trigger={['hover', 'focus']}>
          <span tabIndex={0}>{formatUsageCount(Number(count), language)}</span>
        </Tooltip>
      )}
    />
  );
}
