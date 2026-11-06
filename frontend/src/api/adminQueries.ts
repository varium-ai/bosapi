import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query';
import { apiClient } from './client';
import type { APIResponse, OrganizationOption, PaginatedResult } from './types';
import type { APIKeyRecord } from '../features/keys/types';

const REFERENCE_DATA_STALE_TIME = 5 * 60 * 1000;

export function organizationOptionsQueryOptions() {
  return queryOptions({
    queryKey: ['organizations-select'],
    queryFn: async () => {
      const response = await apiClient.get<APIResponse<PaginatedResult<OrganizationOption>>>(
        '/admin/organizations',
        {
          params: { page: 1, page_size: 100 },
        },
      );
      return response.data?.data?.items || [];
    },
    staleTime: REFERENCE_DATA_STALE_TIME,
  });
}

export function adminKeysQueryOptions({
  page = 1,
  pageSize = 20,
  search = '',
  organizationId,
}: {
  page?: number;
  pageSize?: number;
  search?: string;
  organizationId?: number;
} = {}) {
  return queryOptions({
    queryKey: ['admin-keys', page, pageSize, search, organizationId],
    refetchInterval: 30000,
    queryFn: async () => {
      const response = await apiClient.get<APIResponse<PaginatedResult<APIKeyRecord>>>(
        '/admin/keys',
        {
          params: {
            page,
            page_size: pageSize,
            search: search || undefined,
            organization_id: organizationId || undefined,
          },
        },
      );
      return response.data?.data || { items: [], total: 0 };
    },
  });
}

export interface KeyNameOption {
  id: number;
  name: string;
  organization_id: number;
}
async function fetchKeyNameOptions(organizationId?: number, search = '', page = 1) {
  const response = await apiClient.get<APIResponse<PaginatedResult<KeyNameOption>>>(
    '/admin/keys/options',
    { params: { organization_id: organizationId, search, page, page_size: 100 } },
  );
  return response.data?.data || { items: [], total: 0 };
}
export function keyNameOptionsQueryOptions(organizationId?: number, search = '') {
  return queryOptions({
    queryKey: ['key-name-options', organizationId, search],
    queryFn: () => fetchKeyNameOptions(organizationId, search),
    staleTime: REFERENCE_DATA_STALE_TIME,
  });
}
export function keyNameOptionsInfiniteQueryOptions(organizationId?: number, search = '') {
  return infiniteQueryOptions({
    queryKey: ['key-name-options', 'pages', organizationId, search],
    initialPageParam: 1,
    queryFn: ({ pageParam }) => fetchKeyNameOptions(organizationId, search, pageParam),
    getNextPageParam: (last, pages) =>
      pages.reduce((n, page) => n + page.items.length, 0) < last.total
        ? pages.length + 1
        : undefined,
    staleTime: REFERENCE_DATA_STALE_TIME,
  });
}
