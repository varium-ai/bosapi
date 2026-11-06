import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { snapshotUsable, type UsageSnapshot } from '../usage/UsageSnapshotStatus';

interface IPPage {
  items: { client_ip: string; requests: number; security_events: number; last_seen: string }[];
  total: number;
  snapshot?: UsageSnapshot;
}

export function useKeyIPUsage(keyId: number, enabled: boolean, portal = false) {
  const [windowMinutes, setWindowMinutes] = useState(30);
  const scope = JSON.stringify([keyId, portal, windowMinutes]);
  const endpoint = portal ? '/key-portal/ips' : '/admin/usage/ips';
  const [state, setState] = useState({ scope, page: 1, snapshotId: '' });
  const current = state.scope === scope ? state : { scope, page: 1, snapshotId: '' };
  if (state.scope !== scope) setState(current);
  const { page, snapshotId } = current;
  const query = useQuery<IPPage>({
    queryKey: [
      'key-ip-usage-presets',
      keyId,
      portal,
      windowMinutes,
      page,
      page > 1 ? snapshotId : '',
    ],
    enabled,
    retry: false,
    staleTime: 30000,
    refetchInterval: query => (query.state.data?.snapshot?.status === 'generating' ? 2000 : 30000),
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(endpoint, {
        signal,
        params: {
          key_id: portal ? undefined : keyId,
          window_minutes: windowMinutes,
          page,
          page_size: 20,
          snapshot_id: page > 1 ? snapshotId || undefined : undefined,
        },
      });
      const data = response.data?.data as IPPage;
      if (!Array.isArray(data?.items) || typeof data.total !== 'number') {
        throw new Error('Invalid IP usage response');
      }
      return data;
    },
  });
  useEffect(() => {
    const snapshot = query.data?.snapshot;
    if (snapshot?.status === 'replaced') {
      setState({ scope, page: 1, snapshotId: '' });
    } else if (snapshot?.snapshot_id && snapshotUsable(snapshot)) {
      setState(previous =>
        previous.scope !== scope || previous.snapshotId === snapshot.snapshot_id
          ? previous
          : { ...previous, snapshotId: snapshot.snapshot_id! },
      );
    }
  }, [query.data?.snapshot, scope]);
  return {
    ...query,
    page,
    windowMinutes,
    setWindowMinutes,
    setPage: (page: number) => setState({ ...current, page }),
    usable: snapshotUsable(query.data?.snapshot),
  };
}
