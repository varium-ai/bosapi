import { Alert, Card, Drawer, Empty, Progress, Skeleton, Typography, theme } from 'antd';
import { ClockCircleOutlined, SyncOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { ToolbarButton } from '../common/ToolbarButton';
import styles from './SubscriptionUsageDrawer.module.css';

const platforms = new Set(['openai', 'anthropic', 'gemini', 'antigravity']);

interface ProviderUsageTarget {
  id: number;
  name: string;
  account_type: string;
  platform: string;
}

interface UsageWindow {
  label?: string;
  period?: string;
  unit?: string;
  duration_seconds?: number;
  used_percent: number;
  remaining_percent: number;
  resets_at?: string;
}

interface UsageSnapshot {
  checked_at: string;
  windows: UsageWindow[];
}

export function hasSubscriptionUsage(provider: ProviderUsageTarget) {
  return provider.account_type === 'oauth' && platforms.has(provider.platform);
}

export function SubscriptionUsageDrawer({
  provider,
  onClose,
}: {
  provider: ProviderUsageTarget;
  onClose: () => void;
}) {
  const { t, formatDateTime, formatPercent, formatNumber } = useI18n();
  const { token } = theme.useToken();
  const { data, error, isFetching, isPending, refetch } = useQuery({
    queryKey: ['provider-subscription-usage', provider.id],
    queryFn: async ({ signal }) => {
      const response = await apiClient.post<{ data: UsageSnapshot }>(
        `/admin/providers/${provider.id}/subscription-usage`,
        undefined,
        { signal, silent: true },
      );
      return response.data.data;
    },
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const errorCode = isAxiosError(error) ? error.response?.data?.error?.code : undefined;
  const errorKey = ['provider_auth_failed', 'oauth_exchange_failed'].includes(errorCode)
    ? 'auth_error'
    : errorCode === 'provider_rate_limited'
      ? 'rate_limited'
      : 'error';

  function period(window: UsageWindow) {
    const seconds = window.duration_seconds;
    if (window.period === '5h' || seconds === 18000) return t('providers.usage.five_hours');
    if (window.period === 'weekly' || seconds === 604800) return t('providers.usage.seven_days');
    if (window.period) return window.period;
    if (seconds && seconds > 0) {
      return t('providers.usage.duration_hours').replace(
        '{hours}',
        formatNumber(seconds / 3600, 2),
      );
    }
    return '';
  }

  return (
    <Drawer
      open
      title={
        <div className={styles.heading}>
          <span>{t('providers.usage.title')}</span>
          <Typography.Text type="secondary" className={styles.account}>
            {provider.name}
          </Typography.Text>
        </div>
      }
      onClose={onClose}
      width="min(560px, 100vw)"
      extra={
        <ToolbarButton icon={<SyncOutlined />} loading={isFetching} onClick={() => void refetch()}>
          {t('common.refresh')}
        </ToolbarButton>
      }
      footer={
        data && !error ? (
          <div className={styles.timestamp}>
            <ClockCircleOutlined aria-hidden="true" />
            <span>{t('providers.usage.checked_at')}</span>
            <time dateTime={data.checked_at}>{formatDateTime(data.checked_at)}</time>
          </div>
        ) : undefined
      }
    >
      <div className={styles.windows}>
        <Alert showIcon type="info" message={t('providers.usage.description')} />
        {isPending ? (
          [0, 1].map(index => (
            <Card size="small" key={index}>
              <Skeleton active title={{ width: '32%' }} paragraph={{ rows: 2 }} />
            </Card>
          ))
        ) : error ? (
          <Alert showIcon type="warning" message={t(`providers.usage.${errorKey}`)} />
        ) : data?.windows.length ? (
          data.windows.map((window, index) => {
            const duration = period(window);
            const title = window.label || duration || t('providers.usage.window');
            const meta = [window.label && duration, window.unit].filter(Boolean).join(' · ');
            const percent = Math.min(100, Math.max(0, window.used_percent));
            const strokeColor = percent >= 60 ? 'var(--primary-hover)' : token.colorTextSecondary;
            const used = formatPercent(window.used_percent, 1);
            return (
              <Card size="small" key={index}>
                <div className={styles.cardHeader}>
                  <div className={styles.windowTitle}>
                    <h3>{title}</h3>
                    {meta && <span className={styles.meta}>{meta}</span>}
                  </div>
                  <div className={styles.remaining}>
                    <span>{t('providers.usage.remaining')}</span>
                    <strong>{formatPercent(window.remaining_percent, 1)}</strong>
                  </div>
                </div>
                <Progress
                  className={styles.progress}
                  percent={percent}
                  size="small"
                  showInfo={false}
                  status="normal"
                  strokeColor={strokeColor}
                  trailColor={token.colorFillSecondary}
                  aria-label={`${title} ${t('providers.usage.used')}`}
                  aria-valuetext={used}
                />
                <div className={styles.cardFooter}>
                  <span className={styles.used}>
                    {t('providers.usage.used')} <strong>{used}</strong>
                  </span>
                  {window.resets_at && (
                    <span className={styles.timestamp}>
                      <ClockCircleOutlined aria-hidden="true" />
                      <span>{t('providers.usage.resets_at')}</span>
                      <time dateTime={window.resets_at}>{formatDateTime(window.resets_at)}</time>
                    </span>
                  )}
                </div>
              </Card>
            );
          })
        ) : (
          <Card size="small">
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('providers.usage.empty')} />
          </Card>
        )}
      </div>
    </Drawer>
  );
}
