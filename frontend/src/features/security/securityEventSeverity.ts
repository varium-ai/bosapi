export function getSecurityEventSeverityFilter(value: string | null) {
  if (value === 'all') return { selection: 'all' };
  if (value && ['info', 'warning', 'high', 'critical'].includes(value)) {
    return { selection: value, severity: value };
  }
  return { selection: 'warning_and_above', min_severity: 'warning' };
}
