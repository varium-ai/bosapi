import React from 'react';
import { Tabs } from 'antd';
import { useLocation, useNavigate } from 'react-router-dom';
import { useI18n } from '../i18n';

export const TaskNavigation: React.FC<{ role?: string }> = () => {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const { t } = useI18n();
  const requests = ['/admin/requests', '/admin/errors', '/admin/request-logs'];
  const items = requests.includes(pathname)
    ? requests.map((key, i) => ({ key, label: t(['ux.calls', 'ux.failures', 'ux.trace'][i]) }))
    : [];
  return items.length ? (
    <Tabs
      activeKey={pathname}
      items={items}
      onChange={key => {
        const params = new URLSearchParams(search);
        const supported =
          key === '/admin/errors'
            ? ['organization_id', 'key_id']
            : key === '/admin/request-logs'
              ? ['request_id']
              : ['organization_id', 'key_id', 'from', 'to', 'model'];
        for (const name of Array.from(params.keys()))
          if (!supported.includes(name)) params.delete(name);
        navigate(`${key}${params.size ? `?${params}` : ''}`);
      }}
    />
  ) : null;
};
