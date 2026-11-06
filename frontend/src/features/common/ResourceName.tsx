import React from 'react';
import { Tag } from 'antd';
import { Link, useOutletContext } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { createResourceNameLookup } from './resourceNameLookup';

// Deduplicate names through Query and bound concurrent lookups across table rows.
const lookupName = createResourceNameLookup(apiClient);

export const ResourceName: React.FC<{
  kind: 'organizations' | 'keys';
  id?: number | string | null;
  name?: string;
  link?: boolean;
}> = ({ kind, id, name, link = true }) => {
  const { t } = useI18n();
  const { user } =
    useOutletContext<{ user?: { id: number; role: string; organization_id: number } }>() || {};
  const valid = Number.isSafeInteger(Number(id)) && Number(id) > 0;
  const query = useQuery({
    queryKey: ['resource-name', user?.id, user?.role, user?.organization_id, kind, Number(id)],
    queryFn: () => lookupName(`/admin/${kind}/${Number(id)}`),
    enabled: valid && !name && !!user,
    staleTime: 5 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
    retry: false,
    retryOnMount: false,
    refetchOnWindowFocus: false,
  });
  if (!valid) return <span>-</span>;
  const label = name || query.data;
  if (!label)
    return (
      <span className="resource-name-unavailable">
        {t(query.isFetching ? 'common.loading' : 'ux.name_unavailable')}
      </span>
    );
  const href =
    kind === 'organizations'
      ? `/admin/organizations?search=${encodeURIComponent(label)}`
      : `/admin/keys/${Number(id)}`;
  return (
    <Tag className="resource-name" title={label} bordered={false}>
      {link ? (
        <Link to={href} onClick={event => event.stopPropagation()}>
          {label}
        </Link>
      ) : (
        label
      )}
    </Tag>
  );
};
