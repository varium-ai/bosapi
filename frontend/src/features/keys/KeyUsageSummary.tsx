import { Card, Statistic, Table } from 'antd';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';
import { SegmentedControl } from '../common/SegmentedControl';

export type UsagePeriod = 'total' | 'daily' | 'weekly' | 'monthly';
type Metric =
  'requests' | 'errors' | 'input_tokens' | 'output_tokens' | 'total_tokens' | 'cost_amount';
export type UsagePoint = { period: string } & Partial<Record<Metric, number | string>>;

export function KeyUsageSummary({
  period,
  onPeriodChange,
  points,
  loading,
  failed,
  retry,
  showErrors = false,
  showTotals = true,
  available = true,
}: {
  period: UsagePeriod;
  onPeriodChange: (period: UsagePeriod) => void;
  points: UsagePoint[];
  loading: boolean;
  failed: boolean;
  retry: () => unknown;
  showErrors?: boolean;
  showTotals?: boolean;
  available?: boolean;
}) {
  const { t, formatNumber, formatCurrency } = useI18n();
  const metrics: Metric[] = [
    'requests',
    'input_tokens',
    'output_tokens',
    'total_tokens',
    'cost_amount',
  ];
  if (showErrors) metrics.push('errors');
  return (
    <div className="console-section-stack">
      <SegmentedControl
        aria-label={t('overview.period')}
        value={period}
        onChange={onPeriodChange}
        options={[
          { label: t('usage.period_total'), value: 'total' },
          { label: t('usage.daily_tab'), value: 'daily' },
          { label: t('usage.weekly_tab'), value: 'weekly' },
          { label: t('usage.monthly_tab'), value: 'monthly' },
        ]}
      />
      {failed && <LoadError retry={retry} />}
      {available && (!failed || points.length > 0) && (
        <>
          {showTotals && (
            <div className="console-metric-grid">
              {metrics.map(metric => {
                const value = points.reduce((sum, point) => sum + Number(point[metric] || 0), 0);
                return (
                  <Card size="small" key={metric}>
                    <Statistic
                      title={t(metric === 'cost_amount' ? 'usage.amount' : `usage.${metric}`)}
                      value={value}
                      loading={loading}
                      formatter={() =>
                        metric === 'cost_amount'
                          ? formatCurrency(value, { decimals: 6 })
                          : formatNumber(value)
                      }
                    />
                  </Card>
                );
              })}
            </div>
          )}
          {period !== 'total' && (
            <Table<UsagePoint>
              size="small"
              rowKey="period"
              dataSource={points}
              loading={loading}
              pagination={{ pageSize: 10, showSizeChanger: false }}
              scroll={{ x: 740 }}
              columns={[
                { title: t('usage.period'), dataIndex: 'period', width: 140 },
                ...(['requests', 'input_tokens', 'output_tokens', 'total_tokens'] as const).map(
                  metric => ({
                    title: t(`usage.${metric}`),
                    dataIndex: metric,
                    width: 120,
                    align: 'right' as const,
                    render: (value: number | string) => formatNumber(value),
                  }),
                ),
                {
                  title: t('usage.amount'),
                  dataIndex: 'cost_amount',
                  width: 140,
                  align: 'right',
                  render: (value: number | string) => formatCurrency(value, { decimals: 6 }),
                },
              ]}
            />
          )}
        </>
      )}
    </div>
  );
}
