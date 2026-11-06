import { Table, Typography } from 'antd';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';
import { HttpStatusTag } from '../common/HttpStatusTag';
import { RequestIDLink } from '../requestlogs/RequestIDLink';
import { CursorPagination } from '../common/CursorPagination';

interface ActivityRecord {
  id: number | string;
  created_at: string;
  request_id: string;
  model?: string;
  status_code?: number;
  total_tokens?: number;
  rule_id?: string;
  action?: string;
  http_status?: number;
}

interface ActivityQuery {
  data?: { items?: ActivityRecord[]; total?: number | null };
  isLoading: boolean;
  isError: boolean;
  refetch: () => unknown;
}

export function KeyIPActivity({
  usage,
  security,
  usagePage,
  securityPage,
  onUsagePage,
  onSecurityPage,
  linkRequests = false,
  usageCursor,
}: {
  usage: ActivityQuery;
  security: ActivityQuery;
  usagePage: number;
  securityPage: number;
  onUsagePage: (page: number) => void;
  onSecurityPage: (page: number) => void;
  linkRequests?: boolean;
  usageCursor?: {
    hasNext: boolean;
    loading?: boolean;
    onPrevious: () => void;
    onNext: () => void;
  };
}) {
  const { t, formatDateTime, formatNumber } = useI18n();
  const sharedColumns = [
    {
      title: t('keys.detail_time'),
      dataIndex: 'created_at',
      width: 180,
      render: (value: string) => formatDateTime(value),
    },
    {
      title: t('keys.detail_request_id'),
      dataIndex: 'request_id',
      width: 220,
      render: (value: string) =>
        linkRequests ? (
          <RequestIDLink value={value} />
        ) : (
          <Typography.Text copyable code>
            {value}
          </Typography.Text>
        ),
    },
  ];
  return (
    <div className="key-ip-expanded-container console-section-stack">
      <section>
        <Typography.Text strong>{t('keys.detail_usage_records')}</Typography.Text>
        {usage.isError && <LoadError retry={() => usage.refetch()} />}
        <Table<ActivityRecord>
          tableLayout="fixed"
          size="small"
          rowKey="id"
          dataSource={usage.data?.items || []}
          loading={usage.isLoading}
          scroll={{ x: 760 }}
          pagination={
            usageCursor
              ? false
              : {
                  current: usagePage,
                  pageSize: 20,
                  total: usage.data?.total || 0,
                  onChange: onUsagePage,
                  size: 'small',
                  showSizeChanger: false,
                }
          }
          columns={[
            ...sharedColumns,
            { title: t('keys.detail_model'), dataIndex: 'model', width: 160 },
            {
              title: t('keys.detail_status'),
              dataIndex: 'status_code',
              width: 90,
              render: (value: number) => <HttpStatusTag value={value} />,
            },
            {
              title: t('keys.detail_tokens'),
              dataIndex: 'total_tokens',
              width: 110,
              align: 'right',
              render: (value: number) => formatNumber(value ?? 0),
            },
          ]}
        />
        {usageCursor && <CursorPagination page={usagePage} {...usageCursor} />}
      </section>
      <section>
        <Typography.Text strong>{t('keys.detail_security_records')}</Typography.Text>
        {security.isError && <LoadError retry={() => security.refetch()} />}
        <Table<ActivityRecord>
          tableLayout="fixed"
          size="small"
          rowKey="id"
          dataSource={security.data?.items || []}
          loading={security.isLoading}
          scroll={{ x: 740 }}
          pagination={{
            current: securityPage,
            pageSize: 20,
            total: security.data?.total || 0,
            onChange: onSecurityPage,
            size: 'small',
            showSizeChanger: false,
          }}
          columns={[
            ...sharedColumns,
            { title: t('keys.detail_rule'), dataIndex: 'rule_id', width: 120 },
            { title: t('keys.detail_action'), dataIndex: 'action', width: 110 },
            {
              title: t('keys.detail_status'),
              dataIndex: 'http_status',
              width: 110,
              render: (value: number) => <HttpStatusTag value={value} />,
            },
          ]}
        />
      </section>
    </div>
  );
}
