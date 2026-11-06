export interface RequestLogEntry {
  id?: number;
  created_at: string;
  level: string;
  event: string;
  message: string;
}

// Support quoted current fields and historical key=value messages.
export function logFields(message: string): Record<string, string> {
  const fields: Record<string, string> = {};
  const pattern = /(?:^|\s)([a-z][a-z0-9_]*)=("(?:\\.|[^"\\])*"|[^\s]*)/g;
  for (const match of message.matchAll(pattern)) {
    let value = match[2];
    if (value.startsWith('"')) {
      try {
        value = JSON.parse(value);
      } catch {
        /* Preserve malformed legacy values. */
      }
    }
    if (value && value !== '<nil>') fields[match[1]] = value;
  }
  if (!fields.endpoint && fields.path) fields.endpoint = fields.path;
  return fields;
}

export function errorDiagnosis(code: string): string {
  return (
    (
      {
        provider_unavailable: 'unavailable',
        upstream_error: 'upstream',
        upstream_timeout: 'timeout',
        provider_auth_failed: 'auth',
        model_not_allowed: 'model',
        invalid_provider: 'provider',
        unsupported_conversion: 'conversion',
        unsupported_upstream_feature: 'response_conversion',
        delivery_interrupted: 'delivery',
        invalid_request: 'invalid',
        rate_limited: 'rate',
        rate_limit_exceeded: 'rate',
        quota_exceeded: 'quota',
        unauthorized: 'credential',
        forbidden: 'forbidden',
        internal_error: 'internal',
      } as Record<string, string>
    )[code] || 'unknown'
  );
}

export function entryDiagnosis(row: RequestLogEntry): string | undefined {
  const fields = logFields(row.message);
  if (fields.failure_stage === 'response_conversion') return 'response_conversion';
  if (fields.failure_stage === 'response_write') return 'delivery';
  if (fields.failure_stage === 'response_read') {
    if (fields.error_code === 'upstream_timeout') return 'timeout';
    if (fields.error_reason?.includes('request_canceled')) return 'request_canceled';
    return 'response_read';
  }
  if (row.event === 'gateway.upstream.request_failed') {
    const known = [
      'request_canceled',
      'timeout',
      'dns_error',
      'tls_error',
      'connection_error',
      'transport_error',
    ];
    if (known.includes(fields.reason)) return fields.reason;
    // Older releases emitted only standard Go error text.
    if (/context canceled/.test(row.message)) return 'request_canceled';
    if (/context deadline exceeded|Client\.Timeout|i\/o timeout/.test(row.message))
      return 'timeout';
    return 'transport_error';
  }
  if (row.event.startsWith('gateway.upstream.')) {
    const status = Number(fields.upstream_status || fields.status);
    if (status === 401 || status === 403) return 'auth';
    if (status === 429) return 'rate';
    if (status === 408 || status === 504) return 'timeout';
    if (status === 404) return 'upstream_not_found';
    if (status >= 400) return 'upstream_http';
  }
  if (fields.error_code) return errorDiagnosis(fields.error_code);
  if (['ERROR', 'WARN'].includes(row.level) && fields.reason) return errorDiagnosis(fields.reason);
  if (
    row.event === 'gateway.request.completed' &&
    (fields.outcome === 'error' || Number(fields.result_status || fields.status) >= 400)
  )
    return 'unknown';
  return undefined;
}

export function analyzeTrace(rows: RequestLogEntry[], incomplete = false) {
  const completed = [...rows].reverse().find(row => row.event === 'gateway.request.completed');
  const completedFields = completed ? logFields(completed.message) : {};
  const completedStatus = Number(completedFields.result_status || completedFields.status);
  const completedFailed =
    completedFields.outcome === 'error' || !!completedFields.error_code || completedStatus >= 400;
  const failures = rows.filter(row => !!entryDiagnosis(row));
  const completedIndex = completed ? rows.indexOf(completed) : rows.length - 1;
  const lastAttempt = rows
    .slice(0, completedIndex + 1)
    .map(row => row.event)
    .lastIndexOf('gateway.upstream.request_started');
  const currentFailures = failures.filter(row => rows.indexOf(row) >= lastAttempt);
  // Final gateway conversion/delivery evidence takes precedence over an older
  // retry failure. HTTP 200 does not override an explicit failed stream outcome.
  const finalGatewayFailure =
    completedFailed &&
    completed &&
    (completedFields.failure_stage ||
      ['unsupported_upstream_feature', 'delivery_interrupted'].includes(
        completedFields.error_code,
      ));
  const fault = finalGatewayFailure
    ? completed
    : [...currentFailures].reverse().find(row => row.event.startsWith('gateway.upstream.')) ||
      currentFailures[currentFailures.length - 1];
  let diagnosis =
    completedStatus > 0 && completedStatus < 400 && !completedFailed
      ? 'success'
      : fault
        ? entryDiagnosis(fault)!
        : 'insufficient';
  // A parsed quota identifier in the final gateway code is more specific than
  // the upstream 429 status, which by itself only establishes rate limiting.
  if (diagnosis === 'rate' && completedFields.error_code === 'quota_exceeded') diagnosis = 'quota';
  const response = [...rows]
    .reverse()
    .find(row => row.event === 'gateway.upstream.response_received');
  const evidence = diagnosis === 'success' ? response : fault || response;
  const faultIndex = evidence ? rows.indexOf(evidence) : rows.length - 1;
  const attempt = [...rows.slice(0, faultIndex + 1)]
    .reverse()
    .find(row => row.event === 'gateway.upstream.request_started');
  const fields: Record<string, string> = {};
  // General request context can come from older middleware logs. Transport
  // status and request ID must belong to the selected attempt, never a retry.
  for (const row of rows) {
    const values = logFields(row.message);
    for (const key of ['method', 'endpoint', 'model', 'provider_id'])
      if (values[key]) fields[key] = values[key];
  }
  if (attempt) Object.assign(fields, logFields(attempt.message));
  // Keep response headers from this attempt even if failure occurred later
  // while converting its body. Never borrow headers from a previous retry.
  const attemptStart = attempt ? rows.indexOf(attempt) : 0;
  const attemptResponse = [...rows.slice(attemptStart, faultIndex + 1)]
    .reverse()
    .find(row => row.event === 'gateway.upstream.response_received');
  if (attemptResponse) Object.assign(fields, logFields(attemptResponse.message));
  if (evidence) {
    const values = logFields(evidence.message);
    if (evidence.event === 'gateway.upstream.bad_status' && values.status) {
      values.upstream_status = values.status;
      delete values.status;
    }
    Object.assign(fields, values);
  }
  if (completed) {
    const values = logFields(completed.message);
    for (const key of [
      'status',
      'result_status',
      'outcome',
      'error_code',
      'duration_ms',
      'endpoint',
      'failure_stage',
      'error_param',
      'error_reason',
      'stream_frame',
      'stream_event',
      'stream_block_index',
      'stream_block_type',
    ])
      if (values[key]) fields[key] = values[key];
  }
  return {
    fields,
    fault: diagnosis === 'success' ? undefined : fault,
    diagnosis,
    failures,
    incomplete,
  };
}
