import { normalizeGatewayEndpoint } from '../../config/gatewayEndpoint';

export type KeyImportTarget = 'cc-switch' | 'vary-ai';
export type CodingClientApp = 'claude' | 'codex';
export interface KeyImportOptions {
  app: CodingClientApp;
  name: string;
  endpoint: string;
  apiKey: string;
}

const schemes: Record<KeyImportTarget, string> = { 'cc-switch': 'ccswitch', 'vary-ai': 'varyai' };

// Vary AI 0.2.4 accepts the provider import contract under its own varyai scheme.
export function buildClientImportLink(target: KeyImportTarget, options: KeyImportOptions): string {
  if (target !== 'cc-switch' && target !== 'vary-ai') throw new Error('Unsupported import target');
  if (!['claude', 'codex'].includes(options.app)) throw new Error('Unsupported client');
  if (!options.apiKey.trim() || /[\r\n]/.test(options.apiKey)) throw new Error('Invalid API key');
  const endpoint = normalizeGatewayEndpoint(options.endpoint);
  const params = new URLSearchParams({
    resource: 'provider',
    app: options.app,
    name: `BOS API - ${options.name.trim() || 'API Key'}`,
    homepage: new URL(endpoint).origin,
    // Claude adds /v1/messages itself; Codex appends /responses to its Base URL.
    endpoint: options.app === 'codex' ? `${endpoint}/v1` : endpoint,
    apiKey: options.apiKey,
    enabled: 'true',
  });
  return `${schemes[target]}://v1/import?${params.toString()}`;
}
