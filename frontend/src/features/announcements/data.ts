import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';

export interface Announcement {
  id: number;
  organization_id: number;
  title: string;
  body: string;
  created_at: string;
}

export interface Publication {
  title: string;
  body: string;
}

export function announcementLoadErrorKey(error: unknown) {
  const status = (error as { response?: { status?: number } } | null)?.response?.status;
  return status === 404 ? 'announcements.unavailable' : 'announcements.load_failed';
}

function parseAnnouncementList(payload: unknown): { items: Announcement[]; total: number } {
  const data = payload as { items?: Announcement[] | null; total?: number } | null;
  if (
    !data ||
    !Number.isSafeInteger(data.total) ||
    data.total! < 0 ||
    (!Array.isArray(data.items) && !(data.items === null && data.total === 0))
  )
    throw new Error('Invalid announcement list response');
  return { items: data.items ?? [], total: data.total! };
}

export function useAnnouncements(
  scope: string,
  page: number,
  keyPortal = false,
  management = false,
) {
  const endpoint = keyPortal
    ? '/key-portal/announcements'
    : management
      ? '/admin/announcements/manage'
      : '/admin/announcements';
  return useQuery({
    queryKey: ['announcements', scope, management ? 'manage' : 'overview', page],
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(endpoint, {
        params: { page, page_size: 5 },
        signal,
        silent: true,
      });
      return parseAnnouncementList(response.data.data);
    },
    refetchInterval: 60_000,
    retry: false,
  });
}
