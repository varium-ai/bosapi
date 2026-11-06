import React from 'react';
import { Button, Empty, Skeleton, Space } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';
import { TraceDiagnosis } from '../requestlogs/TraceDiagnosis';
import { validRequestID } from '../common/ux';

export const FailureDetails: React.FC<{ requestID: string }> = ({ requestID }) => {
  const { t } = useI18n();
  const logs = useQuery({
    queryKey: ['admin-request-logs', requestID, 1],
    queryFn: async () => {
      const response = await apiClient.get('/admin/request-logs', {
        params: { request_id: requestID, limit: 100, offset: 0 },
      });
      return response.data?.data || { items: [], has_more: false };
    },
    enabled: validRequestID(requestID),
    staleTime: 30000,
    retry: false,
  });
  if (logs.isLoading) return <Skeleton active paragraph={{ rows: 3 }} />;
  return (
    <div style={{ padding: '8px 16px' }}>
      {logs.isError ? (
        <LoadError retry={() => logs.refetch()} />
      ) : (
        <>
          <TraceDiagnosis rows={logs.data?.items || []} incomplete={!!logs.data?.has_more} />
          {!logs.data?.items?.length && <Empty description={t('request_logs.no_entries')} />}
        </>
      )}
      <Space>
        <Link to={`/admin/request-logs?request_id=${encodeURIComponent(requestID)}`}>
          {t('ux.trace')}
        </Link>
        <Button size="small" loading={logs.isFetching} onClick={() => logs.refetch()}>
          {t('common.refresh')}
        </Button>
      </Space>
    </div>
  );
};
