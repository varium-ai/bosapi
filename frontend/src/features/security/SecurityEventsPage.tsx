import { TableAction } from '../common/TableAction';
import React, { useState } from 'react';
import { Card, Table, Tag, Alert, Button, Select, Tooltip, Drawer } from 'antd';
import { ConsoleToolbar } from '../common/ConsoleToolbar';
import { SearchInput } from '../common/SearchInput';
import { RecordTitle } from '../common/RecordTitle';
import { RecordDetails } from '../common/RecordDetails';
import { SafetyCertificateOutlined } from '@ant-design/icons';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { RequestIDLink } from '../requestlogs/RequestIDLink';
import { OrgKeyCell } from '../common/OrgKeyCell';
import { actionKey, securityEventName } from '../common/ux';
import { LoadError, PageHelp } from '../common/ConsoleUX';
import { useSearchParams } from 'react-router-dom';
import { useDebouncedValue } from '../common/useDebouncedValue';
import { SecurityEventEvidence } from './SecurityEventEvidence';
import { getSecurityEventSeverityFilter } from './securityEventSeverity';
import './security-events.css';

const SecurityEventList: React.FC = () => {
  const { t, formatDateTime, formatNumber, timezoneBadge } = useI18n();
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Math.floor(Number(params.get('page')) || 1));
  const pageSize = [10, 20, 50, 100].includes(Number(params.get('page_size')))
    ? Number(params.get('page_size'))
    : 20;
  const ruleId = params.get('rule_id') || '';
  const toolName = params.get('tool_name') || '';
  const eventType = params.get('event_type') || undefined;
  const { selection: severitySelection, ...severityParams } = getSecurityEventSeverityFilter(
    params.get('severity'),
  );
  const action = params.get('action') || undefined;
  const organizationID = params.get('organization_id') || undefined;
  const keyID = params.get('key_id') || undefined;
  const searchRule = useDebouncedValue(ruleId);
  const searchTool = useDebouncedValue(toolName);
  const filtersPending = searchRule !== ruleId || searchTool !== toolName;
  const filter = (name: string, value?: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(name, value);
    else next.delete(name);
    next.delete('page');
    setParams(next, { replace: true });
  };
  const [detail, setDetail] = useState<any>(null);

  const {
    data: eventsData,
    isLoading,
    isFetching,
    isError,
    refetch,
  } = useQuery({
    queryKey: [
      'security-events',
      page,
      pageSize,
      searchRule,
      searchTool,
      action,
      eventType,
      severitySelection,
      organizationID,
      keyID,
    ],
    enabled: !filtersPending,
    refetchInterval: 30000,
    refetchIntervalInBackground: false,
    queryFn: async () => {
      const res = await apiClient.get('/admin/security-events', {
        params: {
          page,
          page_size: pageSize,
          rule_id: searchRule || undefined,
          tool_name: searchTool || undefined,
          organization_id: organizationID,
          key_id: keyID,
          action,
          event_type: eventType,
          ...severityParams,
        },
      });
      return res.data?.data || { items: [], total: 0 };
    },
  });

  const columns = [
    {
      title: t('security.event_type', 'Event type'),
      dataIndex: 'event_type',
      key: 'event_type',
      width: 140,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: string) => (
        <span style={{ whiteSpace: 'nowrap' }}>{t(`security.type_${value}`, value || '-')}</span>
      ),
    },
    {
      title: t('security.severity', 'Severity'),
      dataIndex: 'severity',
      key: 'severity',
      width: 100,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: string) => (
        <Tag
          color={
            value === 'critical' || value === 'high'
              ? 'red'
              : value === 'warning'
                ? 'orange'
                : 'blue'
          }
        >
          {t(`security.severity_${value}`, value || '-')}
        </Tag>
      ),
    },
    {
      title: t('security.rule_sensitivity', 'Rule sensitivity'),
      dataIndex: 'rule_sensitivity',
      key: 'rule_sensitivity',
      width: 120,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: string) => (
        <Tag color={value === 'high' ? 'red' : 'blue'}>
          {t(`security.sensitivity_${value}`, value || '-')}
        </Tag>
      ),
    },
    {
      title: t('security.tool_name', 'Tool name'),
      dataIndex: 'tool_name',
      key: 'tool_name',
      width: 140,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: string) => <span style={{ whiteSpace: 'nowrap' }}>{value || '-'}</span>,
    },
    {
      title: t('security.rule_scope', 'Rule scope'),
      dataIndex: 'rule_scope',
      key: 'rule_scope',
      width: 120,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: string) => <span style={{ whiteSpace: 'nowrap' }}>{value || '-'}</span>,
    },
    {
      title: t('security.version', 'Rule version'),
      dataIndex: 'rule_version',
      key: 'rule_version',
      width: 100,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: number) => <span style={{ whiteSpace: 'nowrap' }}>{value || '-'}</span>,
    },
    {
      title: `${t('security.col_time', 'Timestamp')} (${timezoneBadge})`,
      dataIndex: 'created_at',
      key: 'created_at',
      width: 205,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (val: string) =>
        val ? (
          <span style={{ whiteSpace: 'nowrap', display: 'inline-block' }}>
            {formatDateTime(val)}
          </span>
        ) : (
          '-'
        ),
    },
    {
      title: t('security.col_req_id', 'Request ID'),
      dataIndex: 'request_id',
      key: 'request_id',
      width: 250,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (id: string) => <RequestIDLink value={id} />,
    },
    {
      title: t('security.col_org_key', 'Org / Key'),
      key: 'org_key',
      width: 340,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, record: any) => (
        <OrgKeyCell
          orgId={record.organization_id ?? record.org_id}
          keyId={record.api_key_id || record.key_id}
        />
      ),
    },
    {
      title: t('security.col_ip', 'Client IP'),
      dataIndex: 'client_ip',
      key: 'client_ip',
      width: 150,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (ip: string) => <span style={{ whiteSpace: 'nowrap' }}>{ip || '-'}</span>,
    },
    {
      title: t('security.col_rule', 'Security Rule'),
      dataIndex: 'rule_id',
      key: 'rule_id',
      width: 220,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (rule: string, record: { rule_name?: string; rule_version?: number }) => (
        <Tooltip
          title={`${t('security.rule_id', 'Rule ID')}: ${rule}${record.rule_version ? ` · ${t('security.version', 'Rule version')}: ${record.rule_version}` : ''}`}
        >
          <Tag
            color="magenta"
            style={{
              maxWidth: 180,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {securityEventName(record.rule_name, rule)}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: t('security.col_action', 'Enforcement Action'),
      dataIndex: 'action',
      key: 'action',
      width: 140,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (action: string, record: any) => (
        <Tag color={action === 'block' ? 'red' : 'blue'}>
          {t(actionKey(action, record.event_type), action)}
        </Tag>
      ),
    },
    {
      title: t('security.col_hit_count', 'Hit Count'),
      dataIndex: 'hit_count',
      key: 'hit_count',
      width: 100,
      align: 'center' as const,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (count: number) => <Tag>{formatNumber(count ?? 1)}</Tag>,
    },
  ];

  return (
    <Card
      title={t('security.title', 'Security & Sensitive Credential Audits')}
      extra={<RefreshButton loading={isFetching} onClick={() => refetch()} />}
    >
      <ConsoleToolbar>
        <Select
          aria-label={t('security.event_type')}
          placeholder={t('security.event_type', 'Event type')}
          value={eventType}
          onChange={value => filter('event_type', value)}
          allowClear
          style={{ width: 170 }}
          options={[
            'authentication',
            'rate_limit',
            'quota',
            'access_policy',
            'tool_call',
            'sensitive_content',
            'model_output',
            'other',
          ].map(value => ({ value, label: t(`security.type_${value}`, value) }))}
        />
        <Select
          aria-label={t('security.severity')}
          placeholder={t('security.severity', 'Severity')}
          value={severitySelection}
          onChange={value => filter('severity', value === 'warning_and_above' ? undefined : value)}
          style={{ width: 170 }}
          options={[
            {
              value: 'warning_and_above',
              label: t('security.severity_warning_and_above', 'Warning or higher'),
            },
            { value: 'all', label: t('security.severity_all', 'All severities') },
            ...['info', 'warning', 'high', 'critical'].map(value => ({
              value,
              label: t(`security.severity_${value}`, value),
            })),
          ]}
        />
        <SearchInput
          aria-label={t('security.tool_name')}
          placeholder={t('security.tool_name', 'Tool name')}
          value={toolName}
          onChange={event => filter('tool_name', event.target.value)}
          style={{ width: 180 }}
        />
        <Select
          aria-label={t('security.col_action')}
          placeholder={t('security.col_action', 'Action')}
          value={action}
          onChange={value => filter('action', value)}
          allowClear
          style={{ width: 140 }}
          options={[
            { value: 'observe', label: t('ux.observe') },
            { value: 'block', label: t('ux.block') },
          ]}
        />
        <SearchInput
          aria-label={t('security.filter_rule')}
          placeholder={t('security.filter_rule', 'Filter rule_id (e.g. secret_key)')}
          value={ruleId}
          onChange={e => {
            filter('rule_id', e.target.value);
          }}
          allowClear
          style={{ width: 220 }}
        />
        <Button onClick={() => setParams({})}>{t('ux.clear')}</Button>
        {(organizationID || keyID) && <OrgKeyCell orgId={organizationID} keyId={keyID} />}
      </ConsoleToolbar>
      <PageHelp note="ux.notifications_note" />
      {isError && <LoadError retry={refetch} />}
      <Alert
        className="security-events-protection-note"
        type="warning"
        message={t(
          'security.protection_desc',
          'Built-in checks inspect content in memory. Findings are recorded with redacted evidence. Tool authorization follows the global mode. Raw tool parameters are not stored.',
        )}
        showIcon
        icon={<SafetyCertificateOutlined />}
      />

      <Table
        className="security-events-table"
        scroll={{ x: 1170 }}
        tableLayout="fixed"
        rowKey="id"
        dataSource={eventsData?.items || []}
        columns={[
          columns[7],
          {
            title: t('ux.summary'),
            key: 'summary',
            width: 300,
            render: (_: any, record: any) => (
              <RecordTitle
                name={securityEventName(record.rule_name, record.rule_id)}
                description={t(`security.type_${record.event_type}`, record.event_type)}
              />
            ),
          },
          columns[9],
          columns[1],
          columns[12],
          {
            title: t('ux.details'),
            key: 'details',
            width: 85,
            render: (_: any, record: any) => (
              <TableAction size="small" onClick={() => setDetail(record)}>
                {t('ux.details')}
              </TableAction>
            ),
          },
        ]}
        loading={isLoading || filtersPending}
        pagination={{
          disabled: filtersPending,
          current: page,
          pageSize,
          total: eventsData?.total || 0,
          onChange: (p, ps) => {
            const next = new URLSearchParams(params);
            next.set('page', String(ps === pageSize ? p : 1));
            next.set('page_size', String(ps));
            setParams(next);
          },
        }}
      />
      <Drawer
        title={t('security.detail_title')}
        open={!!detail}
        onClose={() => setDetail(null)}
        width={640}
        className="ux-details"
      >
        {detail && <SecurityEventEvidence key={detail.id} eventId={detail.id} />}
        <Alert type="info" message={t('ux.notifications_note')} style={{ marginBottom: 16 }} />
        {detail && (
          <RecordDetails
            items={columns.map(column => ({
              key: column.key,
              label: column.title,
              children:
                (column.render as any)?.(
                  (column as any).dataIndex ? detail[(column as any).dataIndex] : undefined,
                  detail,
                ) ?? '-',
            }))}
          />
        )}
      </Drawer>
    </Card>
  );
};

export const SecurityEventsPage: React.FC = () => <SecurityEventList />;
