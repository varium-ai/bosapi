import { HttpStatusTag } from '../common/HttpStatusTag';
import { CursorPagination } from '../common/CursorPagination';
import { useCursorPagination } from '../common/useCursorPagination';
import { compactKey } from '../common/display';
import { ResourceName } from '../common/ResourceName';
import React, { useState } from 'react';
import { Card, Tabs, Table, Button, Space, Typography, Descriptions, Skeleton, Alert } from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { RequestIDLink } from '../requestlogs/RequestIDLink';
import { ConnectionSettings } from '../common/ConnectionSettings';
import { LoadError, PageHelp, StatusTag } from '../common/ConsoleUX';
import { KeySecretActions } from './KeySecretActions';
import { KeyQuotaTable } from './KeyQuotaTable';
import { KeyIPActivity } from './KeyIPActivity';
import { KeyUsageSummary, type UsagePeriod, type UsagePoint } from './KeyUsageSummary';
import { useAdminKeyUsageRecords } from './useAdminKeyUsageRecords';
import { useKeyIPUsage } from './useKeyIPUsage';
import { KeyIPTimeRange } from './KeyIPTimeRange';
import {
  UsageSnapshotStatus,
  snapshotUsable,
  type UsageSnapshot,
} from '../usage/UsageSnapshotStatus';

const { Text } = Typography;
const pageSize = 20;

const KeyUsageDashboard: React.FC<{ keyId: number }> = ({ keyId }) => {
  const [period, setPeriod] = useState<UsagePeriod>('total');
  const summary = useQuery({
    queryKey: ['key-usage-summary', keyId, period],
    retry: false,
    refetchInterval: 30000,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(`/admin/usage/${period}`, {
        params: { key_id: keyId },
        signal,
      });
      return (response.data?.data || { items: [] }) as {
        items: UsagePoint[];
        snapshot?: UsageSnapshot;
      };
    },
  });
  return (
    <>
      <UsageSnapshotStatus snapshot={summary.data?.snapshot} />
      <KeyUsageSummary
        available={snapshotUsable(summary.data?.snapshot)}
        period={period}
        onPeriodChange={setPeriod}
        points={summary.data?.items || []}
        loading={summary.isLoading}
        failed={summary.isError}
        retry={() => summary.refetch()}
        showTotals={period === 'total'}
      />
    </>
  );
};

const KeyIPRequests: React.FC<{ keyId: number; ip: string }> = ({ keyId, ip }) => {
  const [securityPage, setSecurityPage] = useState(1);
  const query = (resource: string, currentPage: number) =>
    apiClient
      .get(`/admin/${resource}`, {
        params: { key_id: keyId, client_ip: ip, page: currentPage, page_size: pageSize },
      })
      .then(response => response.data?.data || { items: [], total: 0 });
  const usage = useAdminKeyUsageRecords(keyId, true, ip);
  const security = useQuery({
    queryKey: ['key-ip-security-events', keyId, ip, securityPage],
    queryFn: () => query('security-events', securityPage),
  });
  return (
    <KeyIPActivity
      usage={usage}
      security={security}
      usagePage={usage.pagination.page}
      securityPage={securityPage}
      onUsagePage={usage.pagination.goToPage}
      usageCursor={{
        hasNext: !!usage.data?.has_more,
        loading: usage.isFetching,
        onPrevious: usage.pagination.previous,
        onNext: () => usage.pagination.next(usage.data?.next_cursor),
      }}
      onSecurityPage={setSecurityPage}
      linkRequests
    />
  );
};

export const KeyDetailPage: React.FC = () => {
  const navigate = useNavigate();
  const { keyId } = useParams();
  const id = Number(keyId);
  const { formatDateTime, t } = useI18n();
  const [pages, setPages] = useState({ id, security: 1 });
  const currentPages = Object.is(pages.id, id) ? pages : { id, security: 1 };
  if (!Object.is(pages.id, id)) setPages(currentPages);
  const securityPage = currentPages.security;
  const setSecurityPage = (page: number) => setPages({ ...currentPages, security: page });
  const [activeTab, setActiveTab] = useState('ips');
  const [section, setSection] = useState('overview');
  const errorPagination = useCursorPagination(String(id));
  const errorCursor = errorPagination.cursor;

  const keyQuery = useQuery({
    queryKey: ['admin-key-info', id],
    retry: false,
    refetchInterval: 30000,
    queryFn: async ({ signal }) => {
      const res = await apiClient.get(`/admin/keys/${id}`, { signal });
      return res.data?.data;
    },
  });
  const keyData = keyQuery.data;

  const query = (path: string, params: any = {}) =>
    apiClient.get(path, { params }).then(r => r.data?.data || { items: [], total: 0 });
  const usage = useAdminKeyUsageRecords(
    id,
    !!keyData && section === 'activity' && activeTab === 'usage',
  );
  const ips = useKeyIPUsage(id, !!keyData && section === 'activity' && activeTab === 'ips');
  const errors = useQuery({
    queryKey: ['key-detail-errors', id, errorCursor],
    enabled: !!keyData && section === 'activity' && activeTab === 'errors',
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get('/admin/errors', {
        params: { key_id: id, limit: pageSize, cursor: errorCursor || undefined },
        signal,
      });
      return response.data?.data || { items: [], next_cursor: '' };
    },
  });
  const security = useQuery({
    queryKey: ['key-detail-security', id, securityPage],
    enabled: !!keyData && section === 'activity' && activeTab === 'security',
    queryFn: () =>
      query('/admin/security-events', { key_id: id, page: securityPage, page_size: pageSize }),
  });
  const activeQuery =
    activeTab === 'usage'
      ? usage
      : activeTab === 'errors'
        ? errors
        : activeTab === 'security'
          ? security
          : ips;

  return (
    <div>
      <Space wrap style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/admin/keys')}>
          {t('keys.detail_back', 'Back to API Keys')}
        </Button>
        <h2 style={{ margin: 0 }}>{keyData?.name || t('keys.col_name', 'Key Name')}</h2>
        {keyData && section !== 'connection' && (
          <KeySecretActions key={id} keyInfo={{ id, name: keyData.name }} />
        )}
      </Space>

      <Tabs
        activeKey={section}
        onChange={setSection}
        items={[
          { key: 'overview', label: t('nav.overview') },
          { key: 'connection', label: t('ux.connection') },
          { key: 'limits', label: t('ux.quotas') },
          { key: 'activity', label: t('ux.requests') },
        ]}
      />
      {keyQuery.isLoading && (
        <Card>
          <Skeleton active />
        </Card>
      )}
      {keyData && (section === 'overview' || section === 'limits') && (
        <Card style={{ marginBottom: 16 }}>
          {section === 'limits' && <PageHelp note="ux.quota_note" />}
          <Space wrap style={{ marginBottom: 16 }}>
            <Button onClick={() => navigate(`/admin/keys?search=${id}`)}>{t('ux.edit_key')}</Button>
            <Button
              onClick={() =>
                navigate(
                  `/admin/rate-limits?organization_id=${keyData.organization_id}&key_id=${id}`,
                )
              }
            >
              {t('ux.configured_limits')}
            </Button>
            <Button
              onClick={() => navigate(`/admin/organizations?search=${keyData.organization_id}`)}
            >
              {t('nav.organizations')}
            </Button>
          </Space>
          {section === 'overview' && (
            <Descriptions
              bordered
              size="small"
              column={{ xs: 1, sm: 2, md: 3 }}
              style={{ marginBottom: 16 }}
            >
              <Descriptions.Item label={t('keys.col_name', 'Key Name')}>
                {keyData.name}
              </Descriptions.Item>
              <Descriptions.Item label={t('keys.col_key', 'Masked Key')}>
                <Text code>{compactKey(keyData)}</Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('keys.col_status', 'Status')}>
                <StatusTag value={keyData.status} />
              </Descriptions.Item>
              <Descriptions.Item label={t('keys.col_created', 'Created At')}>
                {keyData.created_at ? formatDateTime(keyData.created_at) : '-'}
              </Descriptions.Item>
              <Descriptions.Item label={t('keys.col_expires', 'Expires At')}>
                {keyData.expires_at
                  ? formatDateTime(keyData.expires_at)
                  : t('keys.unlimited', 'Never')}
              </Descriptions.Item>
              <Descriptions.Item
                label={t('keys.security_inspection_upstream', 'Upstream security inspection')}
              >
                {keyData.security_inspection_upstream_enabled === false
                  ? t('keys.security_inspection_disabled', 'Disabled')
                  : t('keys.security_inspection_enabled', 'Enabled')}
              </Descriptions.Item>
              <Descriptions.Item
                label={t('keys.security_inspection_downstream', 'Downstream security inspection')}
              >
                {keyData.security_inspection_downstream_enabled === false
                  ? t('keys.security_inspection_disabled', 'Disabled')
                  : t('keys.security_inspection_enabled', 'Enabled')}
              </Descriptions.Item>
              <Descriptions.Item label={t('keys.col_ip_allowlist', 'IP Allowlist')}>
                {keyData.ip_allowlist?.length
                  ? keyData.ip_allowlist.join(', ')
                  : t('keys.any_ip', 'Any IP')}
              </Descriptions.Item>
              <Descriptions.Item label={t('ux.organization')}>
                <ResourceName kind="organizations" id={keyData.organization_id} />
              </Descriptions.Item>
            </Descriptions>
          )}

          {section === 'limits' && (
            <>
              {keyData.usage_stats_status && (
                <Alert
                  style={{ marginBottom: 16 }}
                  type={
                    ['ready', 'refreshing'].includes(keyData.usage_stats_status)
                      ? 'info'
                      : 'warning'
                  }
                  message={
                    keyData.usage_stats_as_of
                      ? `${t('usage.snapshot_as_of')}: ${formatDateTime(keyData.usage_stats_as_of)}`
                      : t('usage.snapshot_generating')
                  }
                  description={
                    ['ready', 'refreshing'].includes(keyData.usage_stats_status)
                      ? undefined
                      : t(
                          keyData.usage_stats_status === 'refresh_failed'
                            ? 'usage.snapshot_refresh_failed'
                            : 'usage.snapshot_expired',
                        )
                  }
                />
              )}
              <KeyQuotaTable data={keyData} />
            </>
          )}
          {section === 'overview' && (
            <>
              <PageHelp note="ux.usage_note" />
              <KeyUsageDashboard keyId={id} />
            </>
          )}
        </Card>
      )}
      {keyQuery.isError && <LoadError retry={() => keyQuery.refetch()} />}
      {section === 'connection' && keyData && (
        <Card>
          <ConnectionSettings
            keyInfo={{ id, name: keyData.name, maskedKey: compactKey(keyData) }}
          />
        </Card>
      )}
      {section === 'activity' && keyData && (
        <Card>
          {activeQuery.isError && <LoadError retry={() => activeQuery.refetch()} />}
          <Tabs
            activeKey={activeTab}
            onChange={setActiveTab}
            destroyOnHidden
            items={[
              {
                key: 'ips',
                label: t('keys.detail_tab_ip_usage', 'IP Usage'),
                children: (
                  <>
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
                        pagination={{
                          current: ips.page,
                          pageSize,
                          showSizeChanger: false,
                          total: ips.data?.total || 0,
                          onChange: ips.setPage,
                        }}
                        expandable={{
                          expandedRowRender: (record: any) => (
                            <KeyIPRequests keyId={id} ip={record.client_ip} />
                          ),
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
                  </>
                ),
              },
              {
                key: 'usage',
                label: t('keys.detail_tab_usage', 'Usage Details'),
                children: (
                  <Table
                    rowKey="id"
                    dataSource={usage.data?.items || []}
                    tableLayout="fixed"
                    loading={usage.isLoading}
                    scroll={{ x: 800 }}
                    pagination={false}
                    footer={() => (
                      <CursorPagination
                        page={usage.pagination.page}
                        hasNext={!!usage.data?.has_more}
                        loading={usage.isFetching}
                        onPrevious={usage.pagination.previous}
                        onNext={() => usage.pagination.next(usage.data?.next_cursor)}
                      />
                    )}
                    columns={[
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
                    ]}
                  />
                ),
              },
              {
                key: 'errors',
                label: t('keys.detail_tab_errors', 'Error Logs'),
                children: (
                  <>
                    <Typography.Paragraph type="secondary">
                      {t('errors.window_minutes').replace('{count}', '5')}
                      {' · '}
                      <Typography.Link onClick={() => navigate(`/admin/errors?key_id=${id}`)}>
                        {t('errors.time_range')}
                      </Typography.Link>
                    </Typography.Paragraph>
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
                          render: (value: string) => <RequestIDLink value={value} />,
                        },
                        {
                          title: t('keys.detail_status', 'Status'),
                          dataIndex: 'http_status',
                          width: 100,
                          render: (value: number) => <HttpStatusTag value={value} />,
                        },
                        {
                          title: t('keys.detail_error', 'Error'),
                          dataIndex: 'error_code',
                          width: 200,
                        },
                      ]}
                    />
                    <CursorPagination
                      page={errorPagination.page}
                      loading={errors.isFetching}
                      hasNext={!!errors.data?.next_cursor}
                      onPrevious={errorPagination.previous}
                      onNext={() => errorPagination.next(errors.data?.next_cursor)}
                    />
                  </>
                ),
              },
              {
                key: 'security',
                label: t('keys.detail_tab_security', 'Security Audits'),
                children: (
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
                ),
              },
            ]}
          />
        </Card>
      )}
    </div>
  );
};
