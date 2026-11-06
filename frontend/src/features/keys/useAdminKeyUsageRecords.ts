import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useCursorPagination } from '../common/useCursorPagination';
import { defaultUsageRange } from '../usage/userUsageFilters';

interface UsageRecord {
  id: number;
  created_at: string;
  request_id: string;
  model: string;
  status_code: number;
  total_tokens: number;
}
interface UsagePage {
  items: UsageRecord[];
  has_more: boolean;
  next_cursor: string;
}

// Both detail records and IP-expanded records use the count-free usage contract.
// Bind cursors to the Key, IP and business day so they cannot cross filter scopes.
export function useAdminKeyUsageRecords(keyId: number, enabled: boolean, clientIP?: string) {
  const [clock, setClock] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 30000);
    return () => window.clearInterval(timer);
  }, []);
  const range = defaultUsageRange(clock);
  const pagination = useCursorPagination(JSON.stringify([keyId, clientIP, range.from, range.to]));
  const query = useQuery({
    queryKey: ['admin-key-usage-records', keyId, clientIP, range.from, range.to, pagination.cursor],
    enabled,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/admin/usage', {
        signal,
        params: {
          key_id: keyId,
          client_ip: clientIP,
          ...range,
          page_size: 20,
          cursor: pagination.cursor || undefined,
        },
      });
      const data = response.data?.data as UsagePage;
      if (
        !Array.isArray(data?.items) ||
        typeof data.has_more !== 'boolean' ||
        typeof data.next_cursor !== 'string'
      ) {
        throw new Error('Invalid usage cursor response');
      }
      return data;
    },
  });
  return { ...query, pagination };
}
