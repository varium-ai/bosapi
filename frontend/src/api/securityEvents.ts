import { queryOptions, type QueryClient } from '@tanstack/react-query';
import { apiClient } from './client';
import type { APIResponse } from './types';

export interface SecurityEventAccount {
  id: number;
  organization_id?: number;
  role: string;
}

export function canReadSecurityEvents(account?: SecurityEventAccount | null) {
  return (
    !!account?.id &&
    (account.role === 'super_admin' ||
      (account.role === 'org_admin' && (account.organization_id || 0) > 0))
  );
}

export function securityEventUnreadQueryKey(account?: SecurityEventAccount | null) {
  return [
    'security-events-unread',
    account?.id,
    account?.role,
    account?.organization_id ?? (account?.role === 'super_admin' ? 0 : undefined),
  ] as const;
}

export function securityEventUnreadQueryOptions(account?: SecurityEventAccount | null) {
  return queryOptions({
    queryKey: securityEventUnreadQueryKey(account),
    enabled: canReadSecurityEvents(account),
    queryFn: async ({ signal }) => {
      if (!canReadSecurityEvents(account)) return 0;
      const response = await apiClient.get<APIResponse<{ unread_count: number }>>(
        '/admin/security-events/unread',
        { signal, silent: true },
      );
      return response.data?.data?.unread_count || 0;
    },
    refetchInterval: 15000,
    refetchIntervalInBackground: false,
  });
}

export async function markSecurityEventsRead(
  queryClient: QueryClient,
  account: SecurityEventAccount,
  signal: AbortSignal,
) {
  if (!canReadSecurityEvents(account) || signal.aborted) return;
  const queryKey = securityEventUnreadQueryKey(account);
  await queryClient.cancelQueries({ queryKey, exact: true });
  if (signal.aborted) return;
  await apiClient.post('/admin/security-events/read', undefined, { signal });
  // A poll may start while the write is in flight. Its old count must not restore the badge.
  await queryClient.cancelQueries({ queryKey, exact: true });
  if (!signal.aborted) queryClient.setQueryData(queryKey, 0);
}
