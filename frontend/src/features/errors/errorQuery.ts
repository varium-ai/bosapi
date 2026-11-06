import { queryOptions } from '@tanstack/react-query';
import { apiClient } from '../../api/client';

export const ERROR_WINDOW_MINUTES = [5, 15, 30, 60, 120, 360, 720, 1440] as const;

export function errorWindowMinutes(value: string | null): number {
  const minutes = Number(value);
  return ERROR_WINDOW_MINUTES.some(option => option === minutes) ? minutes : 5;
}

export interface ErrorQueryFilter {
  window_minutes: number;
  to: string;
  limit: number;
  cursor?: string;
  error_code?: string;
  organization_id?: string;
  key_id?: string;
}

export function errorQueryOptions(filter: ErrorQueryFilter) {
  return queryOptions({
    queryKey: ['error-logs', filter],
    retry: false,
    queryFn: async ({ signal }) => {
      const res = await apiClient.get('/admin/errors', { params: filter, signal });
      return res.data?.data || { items: [], next_cursor: '' };
    },
  });
}
