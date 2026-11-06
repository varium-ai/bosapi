import { useCallback, useEffect, useMemo, useRef } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  canReadSecurityEvents,
  markSecurityEventsRead,
  securityEventUnreadQueryOptions,
  type SecurityEventAccount,
} from '../../api/securityEvents';

export function useSecurityEventUnread(user: SecurityEventAccount | null, pathname: string) {
  const queryClient = useQueryClient();
  const id = user?.id;
  const role = user?.role;
  const organizationID = user?.organization_id ?? (role === 'super_admin' ? 0 : undefined);
  const account = useMemo(
    () =>
      id && role && organizationID !== undefined
        ? { id, organization_id: organizationID, role }
        : null,
    [id, organizationID, role],
  );
  const activeRead = useRef<{
    account: SecurityEventAccount;
    controller: AbortController;
    pending: boolean;
  } | null>(null);
  const unread = useQuery(securityEventUnreadQueryOptions(account));

  useEffect(() => {
    if (!account || !canReadSecurityEvents(account)) return;
    const read = { account, controller: new AbortController(), pending: false };
    activeRead.current = read;
    return () => {
      read.controller.abort();
      if (activeRead.current === read) activeRead.current = null;
    };
  }, [account]);

  const markRead = useCallback(() => {
    const read = activeRead.current;
    if (!read || read.account !== account || read.pending) return;
    read.pending = true;
    void markSecurityEventsRead(queryClient, read.account, read.controller.signal)
      .catch(() => {
        // Keep the unread count on failure; the API client reports the error.
      })
      .finally(() => {
        read.pending = false;
      });
  }, [account, queryClient]);

  useEffect(() => {
    if (pathname === '/admin/security-events') markRead();
  }, [pathname, markRead]);

  return { unreadCount: canReadSecurityEvents(account) ? unread.data || 0 : 0, markRead };
}
