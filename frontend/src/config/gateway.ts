import { queryOptions, useQuery } from '@tanstack/react-query';
import { resolveGatewayConfig } from './gatewayEndpoint';
import type { GatewayConfig } from './gatewayEndpoint';

export type { GatewayConfig } from './gatewayEndpoint';

export function getGatewayConfig(publicEndpoint?: unknown): GatewayConfig {
  return resolveGatewayConfig({
    publicEndpoint,
  });
}

export async function fetchPlatformGatewayConfig(signal?: AbortSignal): Promise<GatewayConfig> {
  const api = ((import.meta as any).env?.VITE_API_BASE_URL || '/api/v1').replace(/\/$/, '');
  const response = await fetch(`${api}/platform/config`, {
    signal,
    credentials: 'omit',
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) throw new Error('Failed to load the API address settings.');
  const payload = await response.json();
  return getGatewayConfig(payload.data?.gateway_endpoint);
}

export function gatewayConfigQueryOptions() {
  return queryOptions({
    queryKey: ['gateway-config'],
    queryFn: ({ signal }) => fetchPlatformGatewayConfig(signal),
    placeholderData: getGatewayConfig(),
    staleTime: 5 * 60_000,
  });
}

export function useGatewayConfig(): GatewayConfig {
  const { data } = useQuery(gatewayConfigQueryOptions());
  return data || getGatewayConfig();
}
