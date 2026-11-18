import { Card, Table, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';

export function KeyPortalModels({ keyId }: { keyId?: number }) {
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ['key-portal-models', keyId],
    queryFn: async ({ signal }) => {
      const response = await apiClient.get<{ data: { items: string[] } }>('/key-portal/models', {
        signal,
        silent: true,
      });
      return response.data.data.items;
    },
  });
  const models = query.data ?? [];
  return (
    <Card title={t('key_portal.models_title')}>
      {query.isError ? (
        <LoadError retry={() => query.refetch()} />
      ) : (
        <Table
          size="small"
          rowKey="id"
          dataSource={models.map(id => ({ id }))}
          loading={query.isLoading}
          locale={{ emptyText: query.isLoading ? null : t('key_portal.models_empty') }}
          pagination={models.length > 8 ? { pageSize: 8, showSizeChanger: false } : false}
          columns={[
            {
              title: t('key_portal.model_id'),
              dataIndex: 'id',
              render: (id: string) => (
                <Typography.Text code copyable style={{ overflowWrap: 'anywhere' }}>
                  {id}
                </Typography.Text>
              ),
            },
          ]}
        />
      )}
    </Card>
  );
}
