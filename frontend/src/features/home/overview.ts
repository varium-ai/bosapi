export type OverviewRange = '7d' | '30d' | 'total';

export interface UsagePoint {
  period: string;
  requests: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cost_amount: number;
}

export interface RecentRequest {
  id: number;
  request_id: string;
  model: string;
  status_code: number;
  total_tokens: number;
  created_at: string;
}

// Recent requests only consume items. The usage endpoint intentionally returns
// total: null with cursor pagination, so it must not use counted-page validation.
export function parseRecentRequests(payload: unknown): RecentRequest[] {
  const items = (payload as { items?: unknown })?.items;
  if (
    !Array.isArray(items) ||
    items.some(
      item =>
        !item ||
        typeof item.id !== 'number' ||
        typeof item.model !== 'string' ||
        typeof item.status_code !== 'number' ||
        typeof item.total_tokens !== 'number' ||
        typeof item.created_at !== 'string',
    )
  )
    throw new Error('Invalid recent requests response');
  return items as RecentRequest[];
}

export interface RuntimeStatus {
  version: string;
  uptime_seconds: number;
  mysql: { status: string; latency_ms: number };
  redis: { status: string };
  queue: { state: string };
  draining: boolean;
}

// Existing admin usage filters accept calendar dates in Asia/Shanghai, not a timezone parameter.
export function getUsageRange(
  range: OverviewRange,
  now = new Date(),
): { from?: string; to?: string } {
  if (range === 'total') return {};
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now);
  const part = (type: string) => parts.find(item => item.type === type)!.value;
  const to = `${part('year')}-${part('month')}-${part('day')}`;
  const start = new Date(`${to}T00:00:00Z`);
  start.setUTCDate(start.getUTCDate() - (range === '7d' ? 6 : 29));
  return { from: start.toISOString().slice(0, 10), to };
}

export function parseUsagePoints(payload: unknown): UsagePoint[] {
  const items = (payload as { items?: unknown })?.items;
  if (!Array.isArray(items)) throw new Error('Invalid usage response');
  const counters = [
    'requests',
    'errors',
    'input_tokens',
    'output_tokens',
    'total_tokens',
    'cost_amount',
  ] as const;
  if (
    items.some(
      item =>
        !item ||
        typeof item.period !== 'string' ||
        counters.some(
          key => typeof item[key] !== 'number' || !Number.isFinite(item[key]) || item[key] < 0,
        ),
    )
  ) {
    throw new Error('Invalid usage counters');
  }
  return items as UsagePoint[];
}

export function sumUsage(points: UsagePoint[]) {
  return points.reduce(
    (sum, point) => ({
      requests: sum.requests + point.requests,
      errors: sum.errors + point.errors,
      input_tokens: sum.input_tokens + point.input_tokens,
      output_tokens: sum.output_tokens + point.output_tokens,
      total_tokens: sum.total_tokens + point.total_tokens,
      cost_amount: sum.cost_amount + point.cost_amount,
    }),
    { requests: 0, errors: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, cost_amount: 0 },
  );
}

export function getErrorRate(summary: { requests: number; errors: number }): number | null {
  return summary.requests === 0 ? null : (summary.errors / summary.requests) * 100;
}

export function runtimeNeedsAttention(status: RuntimeStatus): boolean {
  return (
    status.draining ||
    status.mysql.status !== 'ok' ||
    !['ok', 'not_configured'].includes(status.redis.status) ||
    status.queue.state !== 'running'
  );
}

export function parseRuntimeStatus(payload: unknown): RuntimeStatus {
  const data = payload as RuntimeStatus;
  if (
    !data ||
    typeof data.mysql?.status !== 'string' ||
    typeof data.redis?.status !== 'string' ||
    typeof data.queue?.state !== 'string' ||
    typeof data.uptime_seconds !== 'number' ||
    !Number.isFinite(data.uptime_seconds) ||
    data.uptime_seconds < 0 ||
    typeof data.mysql.latency_ms !== 'number' ||
    !Number.isFinite(data.mysql.latency_ms) ||
    typeof data.draining !== 'boolean'
  ) {
    throw new Error('Invalid status response');
  }
  return data;
}

export function getTrendGeometry(points: UsagePoint[], size = { width: 640, height: 220 }) {
  const plot = { left: 50, right: size.width - 20, top: 24, bottom: size.height - 36 };
  const sorted = [...points].sort((a, b) => a.period.localeCompare(b.period));
  const maximum = Math.max(1, ...sorted.map(point => point.requests));
  const coordinates = sorted.map((point, index) => ({
    ...point,
    x:
      sorted.length === 1
        ? size.width / 2
        : plot.left + (index * (plot.right - plot.left)) / (sorted.length - 1),
    y: plot.bottom - (point.requests / maximum) * (plot.bottom - plot.top),
  }));
  const line = coordinates.map(point => `${point.x},${point.y}`).join(' ');
  return {
    points: coordinates,
    maximum,
    plot,
    line,
    area:
      coordinates.length > 1
        ? `${plot.left},${plot.bottom} ${line} ${plot.right},${plot.bottom}`
        : '',
  };
}
