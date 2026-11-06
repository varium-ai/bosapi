import { HttpStatusTag } from '../common/HttpStatusTag';
import { CursorPagination } from '../common/CursorPagination';
import { useCursorPagination } from '../common/useCursorPagination';
import { compactKey } from '../common/display';
import React, { useState } from 'react';
import { Button, Card, Space, Table, Typography } from 'antd';
import { LogoutOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useLocation } from 'react-router-dom';
import { apiClient, logoutKeyPortal } from '../../api/client';
import { useI18n } from '../../i18n';
import { KeyQuotaTable } from './KeyQuotaTable';
import { KeyIPActivity } from './KeyIPActivity';
import { useKeyIPUsage } from './useKeyIPUsage';
import { KeyIPTimeRange } from './KeyIPTimeRange';
import {
  snapshotUsable,
  UsageSnapshotStatus,
  type UsageSnapshot,
} from '../usage/UsageSnapshotStatus';
import { KeyUsageSummary, type UsagePeriod, type UsagePoint } from './KeyUsageSummary';
import { LoadError } from '../common/ConsoleUX';
import { AnnouncementPanel } from '../announcements/AnnouncementPanel';

const { Text, Title } = Typography;
const pageSize = 20;
type PortalUsageSnapshot = { items: UsagePoint[]; snapshot?: UsageSnapshot };
const UsageDashboard: React.FC = () => {
  const [period, setPeriod] = useState<UsagePeriod>('total');
  const query = useQuery({
    queryKey: ['usage-dashboard', '/key-portal', period],
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(`/key-portal/usage/${period}`, { signal });
      return response.data?.data as PortalUsageSnapshot;
    },
    refetchInterval: query => (query.state.data?.snapshot?.status === 'generating' ? 2000 : 30000),
    refetchIntervalInBackground: false,
  });
  const available = snapshotUsable(query.data?.snapshot);
  return (
    <div className="console-section-stack">
      <UsageSnapshotStatus snapshot={query.data?.snapshot} />
      <KeyUsageSummary
        period={period}
        onPeriodChange={setPeriod}
        points={available ? query.data?.items || [] : []}
        loading={query.isLoading}
        failed={query.isError}
        retry={() => query.refetch()}
        available={available}
        showErrors
      />
    </div>
  );
};

const KeyPortalIPDetails: React.FC<{ ip: string }> = ({ ip }) => {
  const [usagePage, setUsagePage] = useState(1);
  const [securityPage, setSecurityPage] = useState(1);
  const query = (path: string, page: number) =>
    apiClient
      .get(`/key-portal/${path}`, {
        params: { page, page_size: pageSize, client_ip: ip },
      })
      .then(response => response.data?.data || { items: [], total: 0 });
  const usage = useQuery({
    queryKey: ['key-portal-ip-usage', ip, usagePage],
    queryFn: () => query('usage', usagePage),
  });
  const security = useQuery({
    queryKey: ['key-portal-ip-security', ip, securityPage],
    queryFn: () => query('security-events', securityPage),
  });
  return (
    <KeyIPActivity
      usage={usage}
      security={security}
      usagePage={usagePage}
      securityPage={securityPage}
      onUsagePage={setUsagePage}
      onSecurityPage={setSecurityPage}
    />
  );
};

export const KeyPortalPage: React.FC = () => {
  const location = useLocation();
  const { t, formatDateTime } = useI18n();
  const [usagePage, setUsagePage] = useState(1);
  const errorPagination = useCursorPagination();
  const errorCursor = errorPagination.cursor;
  const [securityPage, setSecurityPage] = useState(1);
  const info = (() => {
    try {
      return JSON.parse(localStorage.getItem('key_portal_info') || '{}');
    } catch {
      return {};
    }
  })();
  const infoQuery = useQuery({
    queryKey: ['key-portal-info'],
    queryFn: async () => {
      const res = await apiClient.get('/key-portal/info');
      return res.data?.data;
    },
    initialData: info,
  });
  const currentInfo = infoQuery.data || info;
  const path = location.pathname.replace(/\/$/, '');
  const view = path.split('/').pop() || 'key-portal';
  const fetchData = (resource: string, params: Record<string, unknown>) =>
    apiClient
      .get(`/key-portal/${resource}`, { params })
      .then(r => r.data?.data || { items: [], total: 0 });
  const usage = useQuery({
    queryKey: ['key-portal-usage', usagePage],
    enabled: view === 'usage',
    queryFn: () => fetchData('usage', { page: usagePage, page_size: pageSize }),
  });
  const ips = useKeyIPUsage(Number(currentInfo.id) || 0, view === 'ips', true);
  const errors = useQuery({
    queryKey: ['key-portal-errors', errorCursor],
    enabled: view === 'errors',
    queryFn: () => fetchData('errors', { limit: pageSize, cursor: errorCursor || undefined }),
  });
  const security = useQuery({
    queryKey: ['key-portal-security', securityPage],
    enabled: view === 'security',
    queryFn: () => fetchData('security-events', { page: securityPage, page_size: pageSize }),
  });
  const activeQuery =
    view === 'usage'
      ? usage
      : view === 'ips'
        ? ips
        : view === 'errors'
          ? errors
          : view === 'security'
            ? security
            : infoQuery;
  const usageColumns = [
    {
      title: t('keys.detail_time', 'Time'),
      dataIndex: 'created_at',
      width: 180,
      render: (value: string) => formatDateTime(value),
    },
    { title: t('keys.detail_model', 'Model'), dataIndex: 'model', width: 160 },
    {
      title: t('keys.detail_status', 'Status'),
      dataIndex: 'status_code',
      width: 90,
      render: (value: number) => <HttpStatusTag value={value} />,
    },
    {
      title: t('keys.detail_input_tokens', 'Input'),
      dataIndex: 'input_tokens',
      width: 110,
      align: 'right' as const,
    },
    {
      title: t('keys.detail_output_tokens', 'Output'),
      dataIndex: 'output_tokens',
      width: 110,
      align: 'right' as const,
    },
    {
      title: t('keys.detail_cost', 'Cost'),
      dataIndex: 'cost_amount',
      width: 140,
      align: 'right' as const,
      render: (value: number) => `$${Number(value || 0).toFixed(6)}`,
    },
  ];
  return (
    <div>
      <Space wrap style={{ marginBottom: 20 }} align="center">
        <Space direction="vertical" size={0}>
          <Title level={4} style={{ margin: 0 }}>
            {currentInfo.name || t('auth.key_portal_title', 'API Key Dashboard')}
          </Title>
          <Text type="secondary">{compactKey(currentInfo)}</Text>
        </Space>
        <Button icon={<LogoutOutlined />} onClick={logoutKeyPortal}>
          {t('auth.key_portal_logout', 'Sign out')}
        </Button>
      </Space>
      {activeQuery.isError && <LoadError retry={() => activeQuery.refetch()} />}
      {view === 'key-portal' && (
        <div className="announcement-layout">
          <Space direction="vertical" style={{ display: 'flex' }} size="large">
            <Card title={t('keys.limits_title', 'Quotas & Usage Status')}>
              <KeyQuotaTable data={currentInfo} loading={infoQuery.isLoading} />
            </Card>
            <Card title={t('key_portal.usage_title', 'Usage at a glance')}>
              <UsageDashboard />
            </Card>
          </Space>
          <aside>
            <AnnouncementPanel scope={`key:${currentInfo.id}`} keyPortal />
          </aside>
        </div>
      )}
      {view === 'usage' && (
        <Card title={t('keys.detail_tab_usage', 'Usage Details')}>
          <Space direction="vertical" style={{ display: 'flex' }} size="large">
            <UsageDashboard />
            <Table
              tableLayout="fixed"
              rowKey="id"
              dataSource={usage.data?.items || []}
              loading={usage.isLoading}
              columns={usageColumns}
              scroll={{ x: 790 }}
              pagination={{
                current: usagePage,
                pageSize,
                showSizeChanger: false,
                total: usage.data?.total || 0,
                onChange: setUsagePage,
              }}
            />
          </Space>
        </Card>
      )}
      {view === 'ips' && (
        <Card title={t('keys.detail_tab_ip_usage', 'IP Usage')}>
          <KeyIPTimeRange value={ips.windowMinutes} onChange={ips.setWindowMinutes} />
          <UsageSnapshotStatus snapshot={ips.data?.snapshot} />
          {ips.usable && (
            <Table
              key={ips.windowMinutes}
              rowKey="client_ip"
              dataSource={ips.data?.items || []}
              loading={ips.isLoading}
              tableLayout="fixed"
              scroll={{ x: 'max-content' }}
              expandable={{
                expandedRowRender: (record: any) => <KeyPortalIPDetails ip={record.client_ip} />,
              }}
              pagination={{
                current: ips.page,
                pageSize,
                showSizeChanger: false,
                total: ips.data?.total || 0,
                onChange: ips.setPage,
              }}
              columns={[
                {
                  title: t('keys.detail_ip_address', 'IP Address'),
                  dataIndex: 'client_ip',
                  width: 160,
                },
                {
                  title: t('keys.detail_requests', 'Requests'),
                  dataIndex: 'requests',
                  width: 120,
                  align: 'right' as const,
                },
                {
                  title: t('keys.detail_security_events', 'Security Events'),
                  dataIndex: 'security_events',
                  width: 140,
                  align: 'right' as const,
                },
                {
                  title: t('keys.detail_last_seen', 'Last Seen'),
                  dataIndex: 'last_seen',
                  width: 180,
                  render: (value: string) => formatDateTime(value),
                },
              ]}
            />
          )}
        </Card>
      )}
      {view === 'errors' && (
        <Card title={t('keys.detail_tab_errors', 'Error Logs')}>
          <Table
            tableLayout="fixed"
            rowKey={(row: any) => `${row.time}-${row.request_id}-${row.error_code}`}
            dataSource={errors.data?.items || []}
            loading={errors.isLoading}
            pagination={false}
            scroll={{ x: 720 }}
            columns={[
              {
                title: t('keys.detail_time', 'Time'),
                dataIndex: 'time',
                width: 180,
                render: (value: string) => formatDateTime(value),
              },
              {
                title: t('keys.detail_request_id', 'Request ID'),
                dataIndex: 'request_id',
                width: 240,
                render: (value: string) => (
                  <Text copyable code>
                    {value}
                  </Text>
                ),
              },
              {
                title: t('keys.detail_status', 'Status'),
                dataIndex: 'http_status',
                width: 100,
                render: (value: number) => <HttpStatusTag value={value} />,
              },
              { title: t('keys.detail_error', 'Error'), dataIndex: 'error_code', width: 200 },
            ]}
          />
          <CursorPagination
            page={errorPagination.page}
            loading={errors.isFetching}
            hasNext={!!errors.data?.next_cursor}
            onPrevious={errorPagination.previous}
            onNext={() => errorPagination.next(errors.data?.next_cursor)}
          />
        </Card>
      )}
      {view === 'security' && (
        <Card title={t('keys.detail_tab_security', 'Security Audits')}>
          <Table
            tableLayout="fixed"
            rowKey="id"
            dataSource={security.data?.items || []}
            loading={security.isLoading}
            scroll={{ x: 690 }}
            pagination={{
              current: securityPage,
              pageSize,
              showSizeChanger: false,
              total: security.data?.total || 0,
              onChange: setSecurityPage,
            }}
            columns={[
              {
                title: t('keys.detail_time', 'Time'),
                dataIndex: 'created_at',
                width: 180,
                render: (value: string) => formatDateTime(value),
              },
              { title: t('keys.detail_ip', 'IP'), dataIndex: 'client_ip', width: 140 },
              { title: t('keys.detail_rule', 'Rule'), dataIndex: 'rule_id', width: 140 },
              { title: t('keys.detail_action', 'Action'), dataIndex: 'action', width: 120 },
              {
                title: t('keys.detail_status', 'Status'),
                dataIndex: 'http_status',
                width: 110,
                render: (value: number) => <HttpStatusTag value={value} />,
              },
            ]}
          />
        </Card>
      )}
    </div>
  );
};
