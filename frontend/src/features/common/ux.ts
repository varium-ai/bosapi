export function actionKey(action: string, eventType?: string): string {
  if (action === 'observe') return 'ux.observe';
  if (action === 'block')
    return eventType === 'tool_call'
      ? 'ux.tool_block'
      : ['authentication', 'access_policy', 'rate_limit', 'quota'].includes(eventType || '')
        ? 'ux.request_block'
        : 'ux.block';
  return '';
}
export function eventKey(code: string): string {
  return (
    (
      {
        model_not_allowed: 'ux.access_denied',
        invalid_provider: 'ux.invalid_provider',
        authentication_missing_credential: 'ux.missing_credential',
        quota_exceeded: 'ux.quota_exceeded',
        rate_limit_exceeded: 'ux.rate_limit_exceeded',
      } as Record<string, string>
    )[code] || ''
  );
}
export function stepKey(event: string): string {
  return (
    (
      {
        'gateway.upstream.request_started': 'transport_started',
        'gateway.upstream.request_failed': 'transport_failed',
        'gateway.upstream.response_received': 'transport_response',
        'gateway.model.request.received': 'received',
        'gateway.model.provider.checked': 'checking_provider',
        'gateway.model.provider.selected': 'selected_provider',
        'gateway.model.routing.retry': 'retry_route',
        'gateway.model.routing.selected': 'route_selected',
        'gateway.rate_limit.check': 'limit_check',
        'gateway.rate_limit.allowed': 'limit_allowed',
        'gateway.usage.recorded': 'usage_recorded',
        'gateway.request.completed': 'completed',
      } as Record<string, string>
    )[event] || ''
  );
}
export function validRequestID(value: string): boolean {
  return /^[A-Za-z0-9._-]{1,128}$/.test(value);
}
export function routeTarget(
  providerID: unknown,
  model: string | undefined,
  publicModel: string | undefined,
  providers: { id: number; name: string }[],
  missing: string,
): string {
  if (!providerID) return missing;
  return `${providers.find(p => p.id === Number(providerID))?.name || missing} / ${model?.trim() || publicModel?.trim() || missing}`;
}

// Keep authored rule names; render machine identifiers as sentence-case labels.
export function securityEventName(ruleName: string | undefined, ruleID: string): string {
  const name = ruleName?.trim() || ruleID;
  if (!/^[a-z0-9]+(?:_[a-z0-9]+)+$/.test(name)) return name;
  const label = name.replace(/_/g, ' ');
  return label.charAt(0).toUpperCase() + label.slice(1);
}
