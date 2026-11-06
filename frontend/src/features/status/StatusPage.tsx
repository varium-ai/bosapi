import { TableAction } from '../common/TableAction';
import React, { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Progress,
  Space,
  Statistic,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  Collapse,
} from 'antd';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery } from '@tanstack/react-query';
import { useOutletContext } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { SegmentedControl } from '../common/SegmentedControl';
import {
  bytes,
  chartSegments,
  type HistoryPoint,
  isStale,
  type Metrics,
  type NodeTelemetry,
  numeric,
  parseTelemetry,
  percent,
  rollingErrorRate,
  selectHistory,
  warnings,
} from './telemetry';
import './status.css';

const num = (v: unknown, decimals = 0) =>
  numeric(v)?.toLocaleString(undefined, { maximumFractionDigits: decimals }) ?? '—';
const duration = (v: unknown) =>
  numeric(v) === undefined
    ? '—'
    : `${Math.floor(Number(v) / 86400)}d ${Math.floor((Number(v) % 86400) / 3600)}h ${Math.floor((Number(v) % 3600) / 60)}m`;
const ms = (v: unknown) => (numeric(v) === undefined ? '—' : `${num(v, 2)} ms`);

function State({ value }: { value?: string }) {
  const { t } = useI18n();
  return (
    <Tag
      color={
        ['ok', 'running'].includes(value || '')
          ? 'success'
          : ['not_configured', 'collecting'].includes(value || '')
            ? 'default'
            : 'warning'
      }
    >
      {t(`monitor.${value}`, value || '—')}
    </Tag>
  );
}
function Detail({ items }: { items: [string, React.ReactNode][] }) {
  return (
    <Descriptions
      size="small"
      column={1}
      items={items.map(([label, children]) => ({ key: label, label, children: children ?? '—' }))}
    />
  );
}
function Meter({ value }: { value: unknown }) {
  const n = numeric(value);
  return n === undefined ? (
    <>—</>
  ) : (
    <Progress
      percent={Math.min(100, Math.max(0, n))}
      size="small"
      strokeColor={n >= 85 ? 'var(--warning)' : 'var(--success)'}
      format={() => percent(n)}
    />
  );
}
function Chart({
  title,
  points,
  metric,
  format = num,
}: {
  title: string;
  points: HistoryPoint[];
  metric: string;
  format?: (v: unknown) => string;
}) {
  const { t, formatDateTime } = useI18n();
  const segments = chartSegments(points, metric);
  const values = points.flatMap(p => (numeric(p[metric]) === undefined ? [] : [Number(p[metric])]));
  return (
    <Card size="small" title={title} className="monitor-chart">
      {segments.length ? (
        <>
          <div className="monitor-chart-values">
            <strong>
              {t('monitor.latest')} {format(points[points.length - 1]?.[metric])}
            </strong>
            <span>
              {t('monitor.max')} {format(Math.max(...values))}
            </span>
          </div>
          <svg
            viewBox="0 0 600 124"
            role="img"
            aria-label={`${title}: ${format(points[points.length - 1]?.[metric])}`}
          >
            <path d="M8 12H592 M8 62H592 M8 112H592" stroke="#e8eeec" fill="none" />
            {segments.map((line, i) => (
              <polyline
                key={i}
                points={line}
                stroke="var(--success)"
                strokeWidth="2.5"
                strokeLinecap="round"
                fill="none"
              />
            ))}
            {segments
              .filter(s => !s.includes(' '))
              .map((s, i) => (
                <circle
                  key={i}
                  cx={s.split(',')[0]}
                  cy={s.split(',')[1]}
                  r="3"
                  fill="var(--success)"
                />
              ))}
          </svg>
          <div className="monitor-chart-axis">
            <span>{formatDateTime(points[0].at)}</span>
            <span>{formatDateTime(points[points.length - 1].at)}</span>
          </div>
        </>
      ) : (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t(metric === 'latency_ms' ? 'ux.no_requests' : 'monitor.no_samples')}
        />
      )}
    </Card>
  );
}

export const StatusPage: React.FC = () => {
  const { t, formatDateTime, timezoneBadge } = useI18n();
  const { user } = useOutletContext<{ user?: { id: number; role: string } }>();
  const label = (key: string) => t(`monitor.${key}`);
  const [auto, setAuto] = useState(true);
  const [nodeID, setNodeID] = useState<string>();
  const [window, setWindow] = useState(120);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    // Freshness continues to age while polling is paused, without fetching data.
    const tick = () => setNow(Date.now());
    const timer = globalThis.setInterval(tick, 10000);
    document.addEventListener('visibilitychange', tick);
    return () => {
      globalThis.clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, []);
  const query = useQuery({
    queryKey: ['system-status', user?.id, user?.role],
    enabled: user?.role === 'super_admin',
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/admin/status', {
        signal,
        timeout: 10000,
        validateStatus: code => (code >= 200 && code < 300) || code === 503,
      });
      return parseTelemetry(response.data?.data);
    },
    refetchInterval: auto ? 30000 : false,
    refetchIntervalInBackground: false,
    staleTime: 25000,
    retry: false,
    refetchOnWindowFocus: auto,
    refetchOnReconnect: auto,
  });
  if (user && user.role !== 'super_admin')
    return <Alert type="warning" showIcon message={t('status.forbidden_desc')} />;
  if (!query.data)
    return (
      <Card loading={query.isLoading}>
        <Alert
          type="warning"
          message={t('status.error_title')}
          action={<Button onClick={() => query.refetch()}>{t('common.retry')}</Button>}
        />
      </Card>
    );
  const data = query.data;
  const legacy = !data.sampled_at;
  const stale = query.isError || (!legacy && isStale(data.sampled_at, now));
  const alerts = legacy ? [] : warnings(data, now);
  const node = data.nodes.find(item => item.id === nodeID);
  const history = selectHistory(data.history, window / 2, now);
  const pool = data.mysql.pool || {},
    mysql = data.mysql.metrics || {},
    redis = data.redis.metrics || {},
    process = data.process || {},
    traffic = data.traffic || {};
  const hasErrorWindow = traffic.error_window_seconds === 300;
  const memorySource =
    process.memory_limit_source === 'cgroup'
      ? label('memory_cgroup')
      : process.memory_limit_source === 'host'
        ? label('memory_host')
        : '—';
  const hits = numeric(redis.keyspace_hits),
    misses = numeric(redis.keyspace_misses);
  const hitRatio =
    hits !== undefined && misses !== undefined && hits + misses > 0
      ? (hits / (hits + misses)) * 100
      : undefined;
  const detailWarning = (state?: string) =>
    state !== 'ok' ? <p className="monitor-note">{label('details_unavailable')}</p> : null;
  const filesystemColumns = [
    { title: label('path'), dataIndex: 'path' },
    { title: label('capacity'), dataIndex: 'total_bytes', render: bytes },
    { title: label('available'), dataIndex: 'available_bytes', render: bytes },
    {
      title: label('used'),
      dataIndex: 'used_percent',
      render: (v: unknown) => <Meter value={v} />,
    },
    { title: label('inodes'), dataIndex: 'inodes_percent', render: percent },
  ];
  return (
    <div className="monitor-page">
      <div className="monitor-heading">
        <div>
          <Typography.Title level={3}>{label('title')}</Typography.Title>
          <p>{label('subtitle')}</p>
        </div>
        <Space wrap>
          <Switch checked={auto} onChange={setAuto} aria-label={label('auto')} />
          <span>{label(auto ? 'auto' : 'paused')}</span>
          <RefreshButton loading={query.isFetching} onClick={() => query.refetch()}>
            {label('refresh')}
          </RefreshButton>
        </Space>
      </div>
      <div className="monitor-note">{label('scope')}</div>
      <Collapse
        items={[
          {
            key: 'definitions',
            label: t('ux.monitor_help'),
            children: (
              <>
                <p>{label('retention')}</p>
                <p>{label('traffic_note')}</p>
                <p>{label('application_process_note')}</p>
                <p>{label('node_note')}</p>
              </>
            ),
          },
        ]}
      />
      {legacy && <Alert showIcon type="info" message={label('legacy')} />}
      {stale && <Alert showIcon type="warning" message={label('stale_data')} />}
      {!legacy && (
        <div className="monitor-sample">
          <span>
            {label('updated')}: {formatDateTime(data.sampled_at)} ({timezoneBadge})
          </span>
          <span>
            {label('sample_ms')}: {ms(data.collection_ms)}
          </span>
        </div>
      )}
      {alerts.length > 0 && (
        <Alert
          showIcon
          type="warning"
          message={`${label('attention')} · ${alerts.length}`}
          description={
            <ul className="monitor-alerts">
              {alerts.map((a, i) => (
                <li key={i}>
                  {label(a.key)} {a.target && <strong>{a.target}</strong>}
                </li>
              ))}
            </ul>
          }
        />
      )}
      {!legacy && !stale && !alerts.length && (
        <div className="monitor-ok">
          {label('no_alerts')}
          <span>{label('alerts_note')}</span>
        </div>
      )}
      <div className="monitor-kpis">
        {[
          [label('requests'), num(traffic.rps, 2)],
          [label('errors'), percent(rollingErrorRate(traffic))],
          [label('latency'), ms(traffic.latency_ms)],
          [label('active'), num(traffic.in_flight)],
        ].map(([title, value]) => (
          <Card key={title} size="small">
            <Statistic title={title} value={value} />
          </Card>
        ))}
      </div>
      <div className="monitor-note">
        {hasErrorWindow
          ? `${label('error_window_counts')}: ${num(traffic.error_window_errors)} / ${num(traffic.error_window_requests)}`
          : label('error_window_unavailable')}
      </div>
      <div className="monitor-kpis">
        {[
          [label('process_cpu'), percent(process.cpu_percent)],
          [label('process_rss'), bytes(process.rss_bytes)],
          [label('process_memory'), percent(process.memory_percent)],
          [`${label('process_memory_limit')} (${memorySource})`, bytes(process.memory_limit_bytes)],
        ].map(([title, value]) => (
          <Card key={title} size="small">
            <Statistic title={title} value={value} />
          </Card>
        ))}
      </div>
      <div className="monitor-note">{label('application_process_note')}</div>
      <div className="monitor-section-heading">
        <strong>{label('recent')}</strong>
        <SegmentedControl
          aria-label={label('recent')}
          value={window}
          onChange={v => setWindow(Number(v))}
          options={[
            { label: '15 min', value: 30 },
            { label: '1 h', value: 120 },
          ]}
        />
      </div>
      <div className="monitor-grid">
        <Chart title={label('requests')} points={history} metric="rps" format={v => num(v, 2)} />
        <Chart title={label('latency')} points={history} metric="latency_ms" format={ms} />
      </div>
      <Tabs
        items={[
          {
            key: 'nodes',
            label: `${label('nodes')} (${data.nodes.length})`,
            children: (
              <>
                <p className="monitor-note">{label('node_note')}</p>
                {data.node_source && !['ok', 'not_configured'].includes(data.node_source) && (
                  <Alert
                    type="warning"
                    showIcon
                    message={
                      <>
                        {label('nodes')}: <State value={data.node_source} />
                      </>
                    }
                  />
                )}
                {!data.nodes.length ? (
                  <Card>
                    <Empty description={label('node_empty')} />
                    <p className="monitor-note">{label('node_setup')}</p>
                  </Card>
                ) : (
                  <Table<NodeTelemetry>
                    rowKey="id"
                    pagination={false}
                    scroll={{ x: 850 }}
                    dataSource={data.nodes}
                    columns={[
                      {
                        title: label('node'),
                        dataIndex: 'id',
                        render: (value, record) => (
                          <TableAction
                            className="console-record-title"
                            onClick={() => setNodeID(record.id)}
                          >
                            {value}
                          </TableAction>
                        ),
                      },
                      {
                        title: t('ux.collection_state'),
                        render: (_, n) =>
                          n.stale || isStale(n.sampled_at, now) || n.status !== 'ok' ? (
                            <State
                              value={n.stale || isStale(n.sampled_at, now) ? 'stale' : n.status}
                            />
                          ) : (
                            <Tag color="success">{t('ux.collected')}</Tag>
                          ),
                      },
                      {
                        title: label('cpu'),
                        render: (_, n) => <Meter value={n.cpu_percent} />,
                        width: 160,
                      },
                      {
                        title: t('ux.host_memory'),
                        render: (_, n) => (
                          <>
                            <Meter value={n.memory?.used_percent} />
                            <small>
                              {label('available')}: {bytes(n.memory?.available_bytes)} /{' '}
                              {bytes(n.memory?.total_bytes)}
                            </small>
                          </>
                        ),
                        width: 230,
                      },
                      {
                        title: label('load'),
                        render: (_, n) => n.load?.map(v => num(v, 2)).join(' / ') || '—',
                      },
                      {
                        title: label('processes'),
                        render: (_, n) =>
                          `${num(n.process_count)}${n.processes_truncated ? '+' : ''}`,
                      },
                      {
                        title: label('updated'),
                        dataIndex: 'sampled_at',
                        render: (value: string) => formatDateTime(value),
                      },
                    ]}
                  />
                )}
              </>
            ),
          },
          {
            key: 'middleware',
            label: label('middleware'),
            children: (
              <div className="monitor-grid">
                <Alert
                  type="info"
                  showIcon
                  style={{ gridColumn: '1 / -1' }}
                  message={t('qa.probe_help').replace(
                    '{seconds}',
                    String(data.sample_interval_seconds),
                  )}
                />
                <Card title="MySQL" extra={<State value={data.mysql.status} />}>
                  <Detail
                    items={[
                      [t('qa.probe_latency'), ms(data.mysql.latency_ms)],
                      [
                        label('connections'),
                        `${num(pool.open)} / ${num(pool.in_use)} / ${num(pool.idle)}`,
                      ],
                      [label('max_connections'), num(pool.max_open)],
                      [label('waits'), num(pool.wait_count)],
                      [label('wait_time'), ms(pool.wait_ms)],
                      ...[
                        'threads_connected',
                        'threads_running',
                        'questions',
                        'slow_queries',
                        'aborted_connects',
                      ].map(key => [label(key), num(mysql[key])] as [string, React.ReactNode]),
                    ]}
                  />
                  {detailWarning(data.mysql.details_status)}
                </Card>
                <Card title="Redis" extra={<State value={data.redis.status} />}>
                  <Detail
                    items={[
                      [label('version'), redis.redis_version],
                      [t('qa.probe_latency'), ms(data.redis.latency_ms)],
                      [label('redis_memory'), bytes(redis.used_memory)],
                      [label('redis_rss'), bytes(redis.used_memory_rss)],
                      [label('redis_peak'), bytes(redis.used_memory_peak)],
                      [label('redis_limit'), bytes(redis.maxmemory)],
                      [label('fragmentation'), num(redis.mem_fragmentation_ratio, 2)],
                      [
                        label('clients'),
                        `${num(redis.connected_clients)} / ${num(redis.blocked_clients)}`,
                      ],
                      [label('ops'), num(redis.instantaneous_ops_per_sec)],
                      [label('hit_ratio'), percent(hitRatio)],
                      [
                        label('evictions'),
                        `${num(redis.evicted_keys)} / ${num(redis.expired_keys)}`,
                      ],
                      [label('role'), redis.role],
                      [label('rdb'), redis.rdb_last_bgsave_status],
                      [
                        label('aof'),
                        `${num(redis.aof_enabled)} / ${redis.aof_last_write_status ?? '—'}`,
                      ],
                    ]}
                  />
                  {detailWarning(data.redis.details_status)}
                  <p className="monitor-note">{label('redis_note')}</p>
                </Card>
                <Card title={label('runtime')}>
                  <Detail
                    items={[
                      [label('version'), data.version],
                      ['Go', process.go_version],
                      ['OS / Arch', `${process.os ?? '—'} / ${process.arch ?? '—'}`],
                      [label('pid'), num(process.pid)],
                      [label('uptime'), duration(data.uptime_seconds)],
                      [label('process_cpu'), percent(process.cpu_percent)],
                      [label('process_rss'), bytes(process.rss_bytes)],
                      [label('process_memory'), percent(process.memory_percent)],
                      [
                        label('process_memory_limit'),
                        `${bytes(process.memory_limit_bytes)} (${memorySource})`,
                      ],
                      [label('heap'), bytes(process.heap_bytes)],
                      [label('runtime_memory'), bytes(process.runtime_bytes)],
                      [label('goroutines'), num(process.goroutines)],
                      [label('gc'), num(process.gc_cycles)],
                      [label('cpu_budget'), num(process.gomaxprocs)],
                      [
                        label('p95'),
                        (traffic as Metrics).p95_overflow ? '> 5000 ms' : ms(traffic.p95_ms),
                      ],
                    ]}
                  />
                </Card>
                <Card title={label('queue')} extra={<State value={data.queue.state} />}>
                  <Detail
                    items={[
                      [
                        label('queue_depth'),
                        `${num(data.queue.depth)} / ${num(data.queue.capacity)}`,
                      ],
                      [label('dropped'), num(data.queue.dropped)],
                      [label('failed'), num(data.queue.failed)],
                    ]}
                  />
                  <Meter
                    value={
                      data.queue.capacity
                        ? (Number(data.queue.depth) / data.queue.capacity) * 100
                        : undefined
                    }
                  />
                  <Chart title={label('queue_depth')} points={history} metric="queue_depth" />
                </Card>
                <Chart title={label('heap')} points={history} metric="heap_bytes" format={bytes} />
                <Chart title={label('goroutines')} points={history} metric="goroutines" />
                <Chart
                  title={label('process_cpu')}
                  points={history}
                  metric="cpu_percent"
                  format={percent}
                />
                <Chart
                  title={label('process_rss')}
                  points={history}
                  metric="rss_bytes"
                  format={bytes}
                />
                <Chart
                  title={label('process_memory')}
                  points={history}
                  metric="memory_percent"
                  format={percent}
                />
                <Chart
                  title={label('errors')}
                  points={hasErrorWindow ? history : []}
                  metric="error_rate"
                  format={percent}
                />
                <Chart
                  title={t('qa.probe_latency') + ' · MySQL'}
                  points={history}
                  metric="mysql_latency_ms"
                  format={ms}
                />
              </div>
            ),
          },
        ]}
      />
      <Drawer
        open={!!node}
        onClose={() => setNodeID(undefined)}
        title={node?.id}
        size="large"
        width="min(940px, 100vw)"
      >
        {node && (
          <div className="monitor-page">
            {(node.stale || isStale(node.sampled_at, now)) && (
              <Alert type="warning" message={label('node_stale')} />
            )}
            <Detail
              items={[
                [label('cores'), num(node.cpu_count)],
                [label('uptime'), duration(node.uptime_seconds)],
                [
                  label('memory'),
                  `${bytes(node.memory?.available_bytes)} / ${bytes(node.memory?.total_bytes)} (${label('available')})`,
                ],
                [
                  label('swap'),
                  `${bytes(node.swap?.used_bytes)} / ${bytes(node.swap?.total_bytes)}`,
                ],
                [label('collector_ms'), ms(node.collection_ms)],
                [label('updated'), formatDateTime(node.sampled_at)],
              ]}
            />
            <div className="monitor-grid">
              <Chart
                title={label('cpu')}
                points={selectHistory(node.history || [], window / 2, now)}
                metric="cpu_percent"
                format={percent}
              />
              <Chart
                title={label('memory')}
                points={selectHistory(node.history || [], window / 2, now)}
                metric="memory_percent"
                format={percent}
              />
              <Chart
                title={`${label('network')} · ${label('rx')}`}
                points={selectHistory(node.history || [], window / 2, now)}
                metric="rx_bps"
                format={v => `${bytes(v)}/s`}
              />
              <Chart
                title={`${label('network')} · ${label('tx')}`}
                points={selectHistory(node.history || [], window / 2, now)}
                metric="tx_bps"
                format={v => `${bytes(v)}/s`}
              />
            </div>
            <Card title={label('filesystems')} size="small">
              <Table
                rowKey={v => String(v.path)}
                size="small"
                pagination={false}
                dataSource={node.disks || []}
                columns={filesystemColumns}
                scroll={{ x: 650 }}
              />
            </Card>
            <Card title={label('top_processes')} size="small">
              <p className="monitor-note">{label('process_note')}</p>
              {node.processes_truncated && <Tag color="warning">{label('truncated')}</Tag>}
              <Table
                rowKey={v => String(v.pid)}
                size="small"
                pagination={false}
                dataSource={node.processes || []}
                scroll={{ x: 500 }}
                columns={[
                  { title: label('pid'), dataIndex: 'pid' },
                  { title: label('name'), dataIndex: 'name' },
                  { title: label('state'), dataIndex: 'state' },
                  { title: label('rss'), dataIndex: 'rss_bytes', render: bytes },
                  { title: label('threads'), dataIndex: 'threads' },
                ]}
              />
            </Card>
          </div>
        )}
      </Drawer>
    </div>
  );
};
