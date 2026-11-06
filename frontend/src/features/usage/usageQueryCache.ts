import type { QueryClient, QueryKey } from '@tanstack/react-query';

// Explicit invalidation asks the server to refresh in the background while
// retaining a valid snapshot. Normal page changes reuse the same version.
export function usageRefreshParams(queryClient: QueryClient, queryKey: QueryKey) {
  return queryClient.getQueryState(queryKey)?.isInvalidated ? { refresh: true } : {};
}

export function refreshUsageQueries(queryClient: QueryClient, queryKey: QueryKey) {
  // Discard inactive pages so visiting them later reuses the refreshed server
  // snapshot instead of forcing another full dashboard refresh for every page.
  queryClient.removeQueries({ queryKey, type: 'inactive' });
  return queryClient.invalidateQueries({ queryKey, type: 'active' });
}
