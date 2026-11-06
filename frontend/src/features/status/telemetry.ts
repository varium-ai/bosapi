export type Metrics = Record<string, number | string | null | undefined>;
export type HistoryPoint = { at: string; [key: string]: number | string | null | undefined };
export interface NodeTelemetry {
  id: string;
  sampled_at: string;
  stale: boolean;
  status: string;
  cpu_count: number;
  cpu_percent?: number;
  load?: number[];
  memory?: Metrics;
  swap?: Metrics;
  disks?: Metrics[];
  network?: Metrics;
  processes?: Metrics[];
  process_count: number;
  processes_truncated: boolean;
  uptime_seconds: number;
  collection_ms: number;
  history?: HistoryPoint[];
  errors?: string[];
}
export interface CertificateTelemetry {
  target: string;
  status: string;
  checked_at: string;
  subject?: string;
  issuer?: string;
  not_before?: string;
  not_after?: string;
  days_remaining?: number;
}
export interface Telemetry {
  version: string;
  sampled_at: string;
  sample_interval_seconds: number;
  collection_ms: number;
  uptime_seconds: number;
  draining: boolean;
  mysql: {
    status: string;
    latency_ms?: number;
    details_status?: string;
    pool?: Metrics;
    metrics?: Metrics;
  };
  redis: { status: string; latency_ms?: number; details_status?: string; metrics?: Metrics };
  queue: { state: string; depth?: number; capacity?: number; dropped?: number; failed?: number };
  process?: Metrics;
  traffic?: { [key: string]: number | string | boolean | null | undefined };
  nodes: NodeTelemetry[];
  node_source: string;
  certificates: CertificateTelemetry[];
  history: HistoryPoint[];
}
export const numeric = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;
export function rollingErrorRate(traffic: Telemetry['traffic']): number | undefined {
  const rate = numeric(traffic?.error_rate);
  return traffic?.error_window_seconds === 300 &&
    (numeric(traffic.error_window_requests) ?? 0) > 0 &&
    rate !== undefined &&
    rate >= 0 &&
    rate <= 100
    ? rate
    : undefined;
}
export function bytes(value: unknown): string {
  const n = numeric(value);
  if (n === undefined || n < 0) return '—';
  const unit = n === 0 ? 0 : Math.max(0, Math.min(4, Math.floor(Math.log(n) / Math.log(1024))));
  return `${(n / 1024 ** unit).toFixed(unit === 0 ? 0 : 1)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`;
}
export function percent(value: unknown): string {
  const n = numeric(value);
  return n === undefined ? '—' : `${n.toFixed(1)}%`;
}
export function isStale(at?: string, now = Date.now(), seconds = 90): boolean {
  const stamp = Date.parse(at || '');
  return !Number.isFinite(stamp) || now - stamp > seconds * 1000 || stamp - now > 30000;
}
export function parseTelemetry(value: unknown): Telemetry {
  const data = value as Telemetry;
  if (
    !data ||
    typeof data !== 'object' ||
    !data.mysql?.status ||
    !data.redis?.status ||
    !data.queue?.state
  )
    throw new Error('Invalid telemetry response');
  // Old backends are recognized, but never presented as supplying new telemetry.
  return {
    ...data,
    nodes: Array.isArray(data.nodes) ? data.nodes : [],
    certificates: Array.isArray(data.certificates) ? data.certificates : [],
    history: Array.isArray(data.history) ? data.history : [],
  };
}
export function chartSegments(points: HistoryPoint[], key: string): string[] {
  const valid = points.flatMap(p => (numeric(p[key]) === undefined ? [] : [Number(p[key])]));
  if (!valid.length) return [];
  const max = Math.max(...valid, 1);
  const times = points.map(p => Date.parse(p.at));
  const validTimes = times.filter(Number.isFinite);
  if (!validTimes.length) return [];
  const minTime = Math.min(...validTimes),
    span = Math.max(...validTimes) - minTime;
  const segments: string[] = [];
  let current: string[] = [];
  points.forEach((p, i) => {
    const n = numeric(p[key]);
    if (
      n === undefined ||
      !Number.isFinite(times[i]) ||
      (i > 0 && times[i] - times[i - 1] > 90000)
    ) {
      if (current.length) segments.push(current.join(' '));
      current = [];
    }
    if (n !== undefined && Number.isFinite(times[i]))
      current.push(
        `${span ? 8 + ((times[i] - minTime) / span) * 584 : 300},${112 - (n / max) * 100}`,
      );
  });
  if (current.length) segments.push(current.join(' '));
  return segments;
}
export function selectHistory(
  points: HistoryPoint[],
  minutes: number,
  now = Date.now(),
): HistoryPoint[] {
  return points
    .filter(point => {
      const at = Date.parse(point.at);
      return Number.isFinite(at) && at >= now - minutes * 60000 && at <= now;
    })
    .sort((a, b) => Date.parse(a.at) - Date.parse(b.at))
    .slice(-120);
}
export function warnings(data: Telemetry, now = Date.now()): { key: string; target: string }[] {
  const result: { key: string; target: string }[] = [];
  const add = (key: string, target = '') => result.push({ key, target });
  if (isStale(data.sampled_at, now)) add('stale');
  for (const [name, state] of [
    ['MySQL', data.mysql.status],
    ['Redis', data.redis.status],
    ['Queue', data.queue.state],
  ]) {
    if (!['ok', 'running', 'not_configured'].includes(state))
      add('subsystem_warning', `${name}: ${state}`);
  }
  for (const [name, state] of [
    ['MySQL', data.mysql.details_status],
    ['Redis', data.redis.details_status],
  ]) {
    if (state === 'unavailable') add('details_warning', name);
  }
  if (data.node_source && !['ok', 'not_configured'].includes(data.node_source))
    add('node_source_warning', data.node_source);
  if ((data.queue.capacity || 0) > 0 && (data.queue.depth || 0) / data.queue.capacity! >= 0.8)
    add('queue_warning');
  if ((data.queue.dropped || 0) > 0 || (data.queue.failed || 0) > 0) add('queue_losses');
  if (
    (rollingErrorRate(data.traffic) ?? 0) >= 5 &&
    (numeric(data.traffic?.error_window_requests) ?? 0) >= 20
  )
    add('error_warning');
  if (
    data.redis.metrics?.rdb_last_bgsave_status === 'err' ||
    data.redis.metrics?.aof_last_write_status === 'err'
  )
    add('persistence_warning');
  for (const node of data.nodes) {
    if (node.stale || isStale(node.sampled_at, now)) {
      add('node_stale', node.id);
      continue;
    }
    if (node.status !== 'ok') add('node_partial', node.id);
    if ((node.cpu_percent || 0) >= 90) add('cpu_warning', node.id);
    if (Number(node.memory?.used_percent) >= 90) add('memory_warning', node.id);
    for (const disk of node.disks || [])
      if (Number(disk.used_percent) >= 85 || Number(disk.inodes_percent) >= 85)
        add('disk_warning', `${node.id} ${disk.path}`);
  }
  for (const cert of data.certificates) {
    if (cert.status !== 'ok') add('certificate_warning', `${cert.target}: ${cert.status}`);
    if (isStale(cert.checked_at, now, 900)) add('certificate_stale', cert.target);
  }
  return result;
}
