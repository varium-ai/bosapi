export function formatFirstTokenLatency(
  value: number | null | undefined,
  formatNumber: (value: number) => string,
): string {
  if (value == null || !Number.isFinite(value) || value < 0) return '-';
  return `${formatNumber(value)} ms`;
}
