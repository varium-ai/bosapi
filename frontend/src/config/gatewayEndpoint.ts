export interface GatewayConfig {
  endpoint: string;
  baseUrl: string;
}

export interface GatewayEndpointSettings {
  endpoint: string;
}

export function normalizeGatewayEndpoint(value: string): string {
  const url = new URL(value.trim());
  if (
    !['https:', 'http:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  ) {
    throw new Error(
      'Use an HTTP or HTTPS gateway URL without credentials, query parameters, or a fragment.',
    );
  }
  if (!/^[a-zA-Z0-9._~:/[\]-]+$/.test(url.href)) throw new Error('Invalid gateway URL.');
  return url.href.replace(/\/+$/, '').replace(/\/v1$/, '');
}

export function resolveGatewayConfig(options: { publicEndpoint?: unknown }): GatewayConfig {
  // Only advertise the address saved by this installation's administrator.
  const value = options.publicEndpoint;
  if (typeof value === 'string' && value.trim()) {
    try {
      const endpoint = normalizeGatewayEndpoint(value);
      return { endpoint, baseUrl: `${endpoint}/v1` };
    } catch {
      /* Invalid configuration stays unconfigured instead of advertising an unsafe URL. */
    }
  }
  return { endpoint: '', baseUrl: '' };
}
