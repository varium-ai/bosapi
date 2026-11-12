import { HttpStatusTag } from '../common/HttpStatusTag';
import React, { useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Empty, Skeleton, Spin, Statistic, Table, Tag } from 'antd';
import {
  ArrowRightOutlined,
  BarChartOutlined,
  CloudServerOutlined,
  DollarOutlined,
  KeyOutlined,
  SafetyCertificateOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import { RefreshButton } from '../common/RefreshButton';
import { useIsFetching, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, Navigate, useOutletContext } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import {
  getErrorRate,
  getTrendGeometry,
  getUsageRange,
  parseRuntimeStatus,
  parseRecentRequests,
  parseUsagePoints,
  runtimeNeedsAttention,
  sumUsage,
} from './overview';
import type { OverviewRange, UsagePoint, RecentRequest } from './overview';
import {
  UsageSnapshotStatus,
  snapshotUsable,
  type UsageSnapshot,
} from '../usage/UsageSnapshotStatus';
import './overview.css';
import { parseTelemetry, rollingErrorRate } from '../status/telemetry';
import { actionKey, securityEventName } from '../common/ux';
import { SegmentedControl } from '../common/SegmentedControl';
import { AnnouncementPanel } from '../announcements/AnnouncementPanel';
import { OrganizationQuotaSummary } from './OrganizationQuotaSummary';

interface OverviewUser {
  id: number;
  role: string;
  organization_id: number;
}
interface SecurityEvent {
  id: number;
  rule_id: string;
  rule_name?: string;
  event_type?: string;
  action: string;
  severity: string;
  created_at: string;
}
interface PageData<T> {
  items: T[];
  total: number;
}

async function readPage<T>(endpoint: string, pageSize: number): Promise<PageData<T>> {
  const response = await apiClient.get(endpoint, { params: { page: 1, page_size: pageSize } });
  const data = response.data?.data;
  if (!Array.isArray(data?.items) || typeof data.total !== 'number' || data.total < 0)
    throw new Error('Invalid paginated response');
  return data;
}

const QueryFailure: React.FC<{ retry: () => void; stale?: boolean }> = ({ retry, stale }) => {
  const { t } = useI18n();
  return (
    <Alert
      className="overview-query-error"
      type="warning"
      showIcon
      message={t(stale ? 'overview.stale_data' : 'overview.load_failed')}
      action={
        <Button size="small" onClick={retry}>
          {t('common.retry')}
        </Button>
      }
    />
  );
};

const DetailLink: React.FC<{ to: string }> = ({ to }) => {
  const { t } = useI18n();
  return (
    <Link className="overview-detail-link" to={to}>
      {t('overview.view_details')}
      <ArrowRightOutlined aria-hidden="true" />
    </Link>
  );
};

const RequestTrend: React.FC<{ points: UsagePoint[] }> = ({ points }) => {
  const { t, formatNumber } = useI18n();
  const chartRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 640, height: 220 });
  const hasPoints = points.length > 0;
  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    const observer = new ResizeObserver(([entry]) => {
      const width = Math.round(entry.contentRect.width);
      const height = Math.round(entry.contentRect.height);
      if (width <= 0 || height <= 0) return;
      setSize(current =>
        current.width === width && current.height === height ? current : { width, height },
      );
    });
    observer.observe(chart);
    return () => observer.disconnect();
  }, [hasPoints]);
  const geometry = getTrendGeometry(points, size);
  if (!hasPoints)
    return (
      <Empty
        className="overview-empty"
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={t('overview.no_usage')}
      />
    );
  const labelInterval = Math.max(
    1,
    Math.ceil(geometry.points.length / Math.max(2, Math.floor(size.width / 100))),
  );
  const labels = geometry.points.filter(
    (_, index) =>
      index === 0 || index === geometry.points.length - 1 || index % labelInterval === 0,
  );
  return (
    <figure className="overview-chart">
      <div className="overview-chart-canvas" ref={chartRef}>
        <svg
          viewBox={`0 0 ${size.width} ${size.height}`}
          role="img"
          aria-label={t('overview.trend')}
        >
          {[0, 0.5, 1].map(fraction => (
            <g key={fraction}>
              <line
                x1={geometry.plot.left}
                x2={geometry.plot.right}
                y1={geometry.plot.bottom - (geometry.plot.bottom - geometry.plot.top) * fraction}
                y2={geometry.plot.bottom - (geometry.plot.bottom - geometry.plot.top) * fraction}
                stroke="#ededed"
                strokeDasharray="4 4"
              />
              <text
                x={geometry.plot.left - 10}
                y={geometry.plot.bottom - (geometry.plot.bottom - geometry.plot.top) * fraction + 4}
                textAnchor="end"
                className="overview-chart-label"
              >
                {formatNumber(Math.round(geometry.maximum * fraction))}
              </text>
            </g>
          ))}
          {geometry.area && <polygon points={geometry.area} fill="#f3f4f6" />}
          <polyline
            points={geometry.line}
            fill="none"
            stroke="#404040"
            strokeWidth="2.5"
            strokeLinejoin="round"
          />
          {geometry.points.map(point => (
            <circle
              key={point.period}
              cx={point.x}
              cy={point.y}
              r="3"
              fill="#fff"
              stroke="#404040"
              strokeWidth="2"
            >
              <title>
                {point.period}: {formatNumber(point.requests)} {t('overview.requests')}
              </title>
            </circle>
          ))}
          {labels.map(point => (
            <text
              key={point.period}
              x={point.x}
              y={size.height - 8}
              textAnchor="middle"
              className="overview-chart-label"
            >
              {point.period.slice(5)}
            </text>
          ))}
        </svg>
      </div>
      <figcaption>
        <span className="overview-chart-legend" />
        {t('overview.requests')}
        <span className="overview-muted">UTC</span>
      </figcaption>
    </figure>
  );
};

const ResourceOverview: React.FC<{ scope: string; superAdmin: boolean }> = ({
  scope,
  superAdmin,
}) => {
  const { t, formatNumber } = useI18n();
  const resources = [
    {
      key: 'organizations',
      label: 'nav.organizations',
      icon: <TeamOutlined />,
      path: '/admin/organizations',
    },
    { key: 'keys', label: 'nav.keys', icon: <KeyOutlined />, path: '/admin/keys' },
    ...(superAdmin
      ? [
          {
            key: 'providers',
            label: 'nav.providers',
            icon: <CloudServerOutlined />,
            path: '/admin/providers',
          },
        ]
      : []),
  ];
  const queries = useQueries({
    queries: resources.map(resource => ({
      queryKey: ['overview', scope, 'resource', resource.key],
      queryFn: () => readPage(resource.path, 1),
      staleTime: 60_000,
      retry: false,
    })),
  });
  return (
    <Card
      title={t('overview.resources')}
      extra={<span className="overview-muted overview-small">{t('overview.resources_note')}</span>}
    >
      <div
        className="overview-resource-grid"
        style={{ gridTemplateColumns: `repeat(${resources.length}, minmax(0, 1fr))` }}
      >
        {resources.map((resource, index) => (
          <div className="overview-resource" key={resource.key}>
            <span className="overview-resource-icon">{resource.icon}</span>
            <div className="overview-resource-body">
              <Link to={resource.path}>
                {t(resource.label)}
                <ArrowRightOutlined aria-hidden="true" />
              </Link>
              {queries[index].isLoading ? (
                <Skeleton.Input active size="small" />
              ) : (
                <strong>
                  {queries[index].data ? formatNumber(queries[index].data.total) : '-'}
                </strong>
              )}
              {queries[index].isError && (
                <Button size="small" type="link" onClick={() => queries[index].refetch()}>
                  {t('overview.load_failed')}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
};

const SystemOverview: React.FC<{ scope: string }> = ({ scope }) => {
  const { t, formatNumber } = useI18n();
  const query = useQuery({
    queryKey: ['overview', scope, 'status'],
    queryFn: async () => {
      // The existing endpoint returns measured subsystem data in its 503 success envelope.
      const response = await apiClient.get('/admin/status', {
        validateStatus: status => status === 503 || (status >= 200 && status < 300),
      });
      return parseRuntimeStatus(response.data?.data);
    },
    refetchInterval: 30_000,
    retry: false,
  });
  const status = query.data;
  const uptime = status
    ? `${Math.floor(status.uptime_seconds / 86400)}${t('status.uptime_d')} ${Math.floor((status.uptime_seconds % 86400) / 3600)}${t('status.uptime_h')} ${Math.floor((status.uptime_seconds % 3600) / 60)}${t('status.uptime_m')}`
    : '-';
  const stateLabel = (state: string) => t(`overview.state_${state}`, state);
  return (
    <Card title={t('nav.status')} extra={<DetailLink to="/admin/status" />}>
      {query.isError && <QueryFailure retry={() => query.refetch()} stale={Boolean(status)} />}
      {query.isLoading ? (
        <Skeleton active paragraph={{ rows: 5 }} />
      ) : (
        status && (
          <>
            <div
              className={`overview-runtime-banner ${runtimeNeedsAttention(status) ? 'needs-attention' : ''}`}
            >
              <span className="overview-status-dot" />
              <strong>
                {t(
                  runtimeNeedsAttention(status)
                    ? 'overview.needs_attention'
                    : 'overview.runtime_ok',
                )}
              </strong>
              <span>{t('overview.runtime_scope')}</span>
            </div>
            <dl className="overview-status-list">
              <div>
                <dt>MySQL</dt>
                <dd>
                  <Tag color={status.mysql.status === 'ok' ? 'success' : 'error'}>
                    {stateLabel(status.mysql.status)}
                  </Tag>
                  <span className="overview-muted">{formatNumber(status.mysql.latency_ms)} ms</span>
                </dd>
              </div>
              <div>
                <dt>Redis</dt>
                <dd>
                  <Tag
                    color={
                      status.redis.status === 'ok'
                        ? 'success'
                        : status.redis.status === 'unavailable'
                          ? 'error'
                          : 'default'
                    }
                  >
                    {stateLabel(status.redis.status)}
                  </Tag>
                </dd>
              </div>
              <div>
                <dt>{t('status.card_queue')}</dt>
                <dd>
                  <Tag color={status.queue.state === 'running' ? 'success' : 'warning'}>
                    {stateLabel(status.queue.state)}
                  </Tag>
                </dd>
              </div>
              <div>
                <dt>{t('status.card_uptime')}</dt>
                <dd>{uptime}</dd>
              </div>
              <div>
                <dt>{t('status.version_label').replace(/[:：]\s*$/, '')}</dt>
                <dd>{status.version || '-'}</dd>
              </div>
            </dl>
            <p className="overview-section-note">{t('overview.status_refresh')}</p>
          </>
        )
      )}
    </Card>
  );
};

const AdminOverview: React.FC<{ user: OverviewUser }> = ({ user }) => {
  const { t, formatNumber, formatCurrency, formatDateTime, timezoneBadge } = useI18n();
  const [range, setRange] = useState<OverviewRange>('7d');
  const scope = `${user.role}:${user.organization_id}:${user.id}`;
  const superAdmin = user.role === 'super_admin';
  const params = getUsageRange(range);
  const client = useQueryClient();
  const refreshing = useIsFetching({ queryKey: ['overview', scope] }) > 0;
  const usage = useQuery({
    queryKey: ['overview', scope, 'usage-total', range, params.from, params.to],
    queryFn: async ({ signal }) => {
      const payload = (await apiClient.get('/admin/usage/total', { params, signal })).data?.data;
      return {
        items: parseUsagePoints(payload),
        snapshot: payload.snapshot as UsageSnapshot | undefined,
      };
    },
    refetchInterval: 60_000,
    retry: false,
  });
  // The daily endpoint is capped at 1000 buckets; keep the chart explicitly bounded even for all-time totals.
  const trendParams = getUsageRange(range === 'total' ? '30d' : range);
  const trend = useQuery({
    queryKey: ['overview', scope, 'usage-daily', trendParams.from, trendParams.to],
    queryFn: async ({ signal }) => {
      const payload = (await apiClient.get('/admin/usage/daily', { params: trendParams, signal }))
        .data?.data;
      return {
        items: parseUsagePoints(payload),
        snapshot: payload.snapshot as UsageSnapshot | undefined,
      };
    },
    refetchInterval: 60_000,
    retry: false,
  });
  const recent = useQuery({
    queryKey: ['overview', scope, 'recent-usage'],
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/admin/usage', { params: { page_size: 5 }, signal });
      return parseRecentRequests(response.data?.data);
    },
    refetchInterval: 60_000,
    retry: false,
  });
  const security = useQuery({
    queryKey: ['overview', scope, 'recent-security'],
    queryFn: () => readPage<SecurityEvent>('/admin/security-events', 4),
    refetchInterval: 60_000,
    retry: false,
  });
  const trafficQuery = useQuery({
    queryKey: ['system-status', user.id, user.role],
    enabled: superAdmin,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/admin/status', {
        signal,
        timeout: 10000,
        validateStatus: code => (code >= 200 && code < 300) || code === 503,
      });
      return parseTelemetry(response.data?.data);
    },
    refetchInterval: 30_000,
    staleTime: 25_000,
    retry: false,
  });
  const summary =
    usage.data && snapshotUsable(usage.data.snapshot) ? sumUsage(usage.data.items) : undefined;
  const errorRate = superAdmin
    ? (rollingErrorRate(trafficQuery.data?.traffic) ?? null)
    : summary
      ? getErrorRate(summary)
      : null;
  const metrics = [
    {
      key: 'requests',
      label: t('overview.requests'),
      value: summary ? formatNumber(summary.requests) : '-',
      note: t('overview.recorded_requests'),
      icon: <BarChartOutlined />,
    },
    {
      key: 'tokens',
      label: t('overview.tokens'),
      value: summary ? formatNumber(summary.total_tokens) : '-',
      note: summary
        ? `${t('overview.input')} ${formatNumber(summary.input_tokens)} / ${t('overview.output')} ${formatNumber(summary.output_tokens)}`
        : t('overview.input_output'),
      icon: <CloudServerOutlined />,
    },
    {
      key: 'cost',
      label: t('overview.cost'),
      value: summary ? formatCurrency(summary.cost_amount, { currency: 'USD', decimals: 6 }) : '-',
      note: t('overview.recorded_cost'),
      icon: <DollarOutlined />,
    },
    {
      key: 'errors',
      label: superAdmin ? t('monitor.errors') : t('overview.error_rate'),
      value: errorRate === null ? '-' : `${formatNumber(errorRate, 2)}%`,
      note: superAdmin
        ? t('overview.traffic_window')
        : summary
          ? `${formatNumber(summary.errors)} ${t('overview.http_errors')}`
          : t('overview.http_error_definition'),
      icon: <SafetyCertificateOutlined />,
    },
  ];

  return (
    <div className="overview-page">
      <div className="overview-toolbar">
        <div>
          <h1>{t('overview.title')}</h1>
          <p>{t(superAdmin ? 'overview.platform_scope' : 'overview.organization_scope')}</p>
        </div>
        <div className="overview-toolbar-actions">
          <SegmentedControl
            aria-label={t('overview.period')}
            value={range}
            onChange={value => setRange(value as OverviewRange)}
            options={[
              { value: '7d', label: t('overview.last_7_days') },
              { value: '30d', label: t('overview.last_30_days') },
              { value: 'total', label: t('overview.all_time') },
            ]}
          />
          <RefreshButton
            loading={refreshing}
            onClick={() => {
              void client.invalidateQueries({ queryKey: ['overview', scope] });
              if (superAdmin) void trafficQuery.refetch();
            }}
          />
        </div>
      </div>
      {trafficQuery.isError && (
        <QueryFailure retry={() => trafficQuery.refetch()} stale={Boolean(trafficQuery.data)} />
      )}
      {usage.isError && <QueryFailure retry={() => usage.refetch()} stale={Boolean(usage.data)} />}
      <UsageSnapshotStatus snapshot={usage.data?.snapshot} />
      <div className="overview-summary-grid">
        {metrics.map(metric => (
          <Card key={metric.key}>
            <div className="overview-metric-label">
              {metric.label}
              <span>{metric.icon}</span>
            </div>
            {usage.isLoading ? (
              <Skeleton.Input active />
            ) : (
              <Statistic
                value={metric.value}
                valueStyle={{
                  color: metric.key === 'errors' && summary?.errors ? '#b45309' : '#171717',
                }}
              />
            )}
            <p className="overview-metric-note">{metric.note}</p>
          </Card>
        ))}
      </div>
      {!superAdmin && user.organization_id > 0 && <OrganizationQuotaSummary user={user} />}

      <div className="overview-data-note">
        <span>{t('overview.time_basis')}</span>
        <span>
          {summary && (usage.data?.snapshot?.generated_at || usage.dataUpdatedAt)
            ? `${t('overview.updated_at')} ${formatDateTime(usage.data?.snapshot?.generated_at || usage.dataUpdatedAt)} (${timezoneBadge})`
            : t('overview.waiting_data')}
        </span>
      </div>

      <div className={`overview-main-grid ${!superAdmin ? 'is-organization' : ''}`}>
        <Card
          title={t('overview.trend')}
          extra={
            <DetailLink
              to={`/admin/usage?${new URLSearchParams(params as Record<string, string>)}`}
            />
          }
        >
          <div className="overview-trend-caption">
            <span>
              {t(
                range === 'total'
                  ? 'overview.trend_30_days'
                  : range === '7d'
                    ? 'overview.last_7_days'
                    : 'overview.last_30_days',
              )}
            </span>
            <span className="overview-muted">
              {trendParams.from} — {trendParams.to}
            </span>
          </div>
          {trend.isError && (
            <QueryFailure retry={() => trend.refetch()} stale={Boolean(trend.data)} />
          )}
          <UsageSnapshotStatus snapshot={trend.data?.snapshot} />
          {trend.isLoading ? (
            <Skeleton active paragraph={{ rows: 5 }} />
          ) : (
            trend.data &&
            snapshotUsable(trend.data.snapshot) && <RequestTrend points={trend.data.items} />
          )}
        </Card>
        {superAdmin && <SystemOverview scope={scope} />}
      </div>

      <ResourceOverview scope={scope} superAdmin={superAdmin} />

      <div className="overview-main-grid">
        <Card title={t('overview.recent_requests')} extra={<DetailLink to="/admin/requests" />}>
          {recent.isError && (
            <QueryFailure retry={() => recent.refetch()} stale={Boolean(recent.data)} />
          )}
          {(recent.data || recent.isLoading) && (
            <Table<RecentRequest>
              className="overview-recent-table"
              size="small"
              rowKey="id"
              dataSource={recent.data || []}
              loading={recent.isLoading}
              pagination={false}
              scroll={recent.data?.length ? { x: 550 } : undefined}
              locale={{ emptyText: t('overview.no_recent_requests') }}
              columns={[
                { title: t('usage.col_model'), dataIndex: 'model', ellipsis: true, width: 170 },
                {
                  title: t('usage.col_status'),
                  dataIndex: 'status_code',
                  width: 85,
                  render: (code: number) => <HttpStatusTag value={code} />,
                },
                {
                  title: t('usage.col_tokens'),
                  dataIndex: 'total_tokens',
                  width: 100,
                  align: 'right' as const,
                  render: (value: number) => formatNumber(value),
                },
                {
                  title: `${t('usage.col_time')} (${timezoneBadge})`,
                  dataIndex: 'created_at',
                  width: 175,
                  render: (value: string) => formatDateTime(value),
                },
              ]}
            />
          )}
          <p className="overview-section-note">{t('overview.recent_note')}</p>
        </Card>
        <Card
          title={t('overview.recent_security')}
          extra={<DetailLink to="/admin/security-events" />}
        >
          {security.isError && (
            <QueryFailure retry={() => security.refetch()} stale={Boolean(security.data)} />
          )}
          {security.isLoading ? (
            <Skeleton active paragraph={{ rows: 4 }} />
          ) : (
            security.data &&
            (security.data.items.length ? (
              <div className="overview-event-list">
                {security.data.items.map(event => (
                  <div className="overview-event" key={event.id}>
                    <span
                      className={`overview-event-dot ${event.action === 'block' ? 'is-blocked' : ''}`}
                    />
                    <div>
                      <strong>{securityEventName(event.rule_name, event.rule_id)}</strong>
                      <span>{formatDateTime(event.created_at)}</span>
                    </div>
                    <Tag color={event.action === 'block' ? 'error' : 'default'}>
                      {t(
                        actionKey(event.action, event.event_type) ||
                          `overview.action_${event.action}`,
                        event.action,
                      )}
                    </Tag>
                  </div>
                ))}
              </div>
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t('overview.no_security_events')}
              />
            ))
          )}
          <p className="overview-section-note">{t('overview.recent_note')}</p>
        </Card>
      </div>
    </div>
  );
};

export const HomePage: React.FC = () => {
  const { user } = useOutletContext<{ user: OverviewUser | null }>();
  if (!user)
    return (
      <div className="overview-loading">
        <Spin />
      </div>
    );
  if (user.role !== 'super_admin' && user.role !== 'org_admin')
    return <Navigate to="/key-portal" replace />;
  return (
    <div className="announcement-layout">
      <AdminOverview user={user} />
      <aside>
        <AnnouncementPanel scope={`${user.role}:${user.organization_id}:${user.id}`} />
      </aside>
    </div>
  );
};
