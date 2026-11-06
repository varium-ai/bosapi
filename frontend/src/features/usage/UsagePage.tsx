import { CursorPagination } from '../common/CursorPagination';
import { UsageSnapshotStatus, snapshotUsable } from './UsageSnapshotStatus';
import { HttpStatusTag } from '../common/HttpStatusTag';
import { ConsoleToolbar } from '../common/ConsoleToolbar';
import { TableAction } from '../common/TableAction';
import { KeySelect } from '../common/KeySelect';
import React, { useState } from 'react';
import {
  Card,
  Table,
  Select,
  Tag,
  Alert,
  Row,
  Col,
  Statistic,
  Input,
  Tabs,
  Button,
  Drawer,
  Descriptions,
  DatePicker,
  Tooltip,
} from 'antd';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery, useQueryClient, useIsFetching } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { organizationOptionsQueryOptions } from '../../api/adminQueries';
import { useI18n } from '../../i18n';
import { RequestIDLink } from '../requestlogs/RequestIDLink';
import { OrgKeyCell } from '../common/OrgKeyCell';
import { ResourceName } from '../common/ResourceName';
import { RecordDetails } from '../common/RecordDetails';
import { useSearchParams } from 'react-router-dom';
import { LoadError, PageHelp } from '../common/ConsoleUX';
import { useDebouncedValue } from '../common/useDebouncedValue';
import { DailyOrganizationUsage } from './DailyOrganizationUsage';
import { UsageCountStatistic } from './UsageCountStatistic';
import { defaultUsageRange, validUserUsageRange } from './userUsageFilters';
import { formatFirstTokenLatency } from './requestTiming';
import { refreshUsageQueries, usageRefreshParams } from './usageQueryCache';

export const UsagePage: React.FC<{ recordsOnly?: boolean }> = ({ recordsOnly = false }) => {
  const [params, setParams] = useSearchParams();
  const queryClient = useQueryClient();
  const fetchingOrganizations = useIsFetching({ queryKey: ['admin-organization-daily-usage'] });
  const [detail, setDetail] = useState<any>(null);
  const { t, formatDateTime, formatNumber, formatTokens, formatCurrency, timezoneBadge } =
    useI18n();
  const selectedOrg = Number(params.get('organization_id')) || undefined;
  const selectedKey = params.get('key_id') || '';
  const modelFilter = params.get('model') || '';
  const searchModel = useDebouncedValue(modelFilter);
  const searchKey = selectedKey;
  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete('page');
    next.delete('cursor');
    setParams(next, { replace: true });
  };
  const setSelectedOrg = (value?: number) => {
    const next = new URLSearchParams(params);
    if (value) next.set('organization_id', String(value));
    else next.delete('organization_id');
    next.delete('key_id');
    next.delete('page');
    next.delete('cursor');
    setParams(next, { replace: true });
  };
  const setSelectedKey = (value: string) => updateFilter('key_id', value);
  const setModelFilter = (value: string) => updateFilter('model', value);
  const activeTab = recordsOnly
    ? 'records'
    : ['records', 'daily', 'weekly', 'monthly'].includes(params.get('view') || '')
      ? params.get('view')!
      : 'organizations';
  const page = Math.max(1, Math.floor(Number(params.get('page')) || 1));
  const pageSize = [10, 20, 50, 100].includes(Number(params.get('page_size')))
    ? Number(params.get('page_size'))
    : 20;

  const [isWideScreen, setIsWideScreen] = useState(() => {
    if (typeof window === 'undefined') return true;
    return window.innerWidth >= 1100;
  });

  React.useEffect(() => {
    if (typeof window === 'undefined') return;
    const mql = window.matchMedia('(min-width: 1100px)');
    const handler = (e: MediaQueryListEvent) => setIsWideScreen(e.matches);
    setIsWideScreen(mql.matches);
    mql.addEventListener('change', handler);
    return () => mql.removeEventListener('change', handler);
  }, []);

  const buildParams = () => {
    const params: any = {};
    if (selectedOrg) params.organization_id = selectedOrg;
    if (searchKey && Number(searchKey) > 0) params.key_id = Number(searchKey);
    if (searchModel.trim()) params.model = searchModel.trim();
    if (from) params.from = from;
    if (to) params.to = to;
    return params;
  };

  const [clock, setClock] = useState(() => new Date());
  React.useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 30000);
    return () => window.clearInterval(timer);
  }, []);
  const defaultRange = defaultUsageRange(clock);
  const previousDay = React.useRef(defaultRange.from);
  React.useEffect(() => {
    if (previousDay.current !== defaultRange.from && !params.has('from') && !params.has('to')) {
      const next = new URLSearchParams(params);
      next.delete('page');
      next.delete('cursor');
      setParams(next, { replace: true });
    }
    previousDay.current = defaultRange.from;
  }, [defaultRange.from, params, setParams]);
  const from = params.get('from') || defaultRange.from;
  const to = params.get('to') || defaultRange.to;
  const validRange = validUserUsageRange(from, to);
  const cursor = params.get('cursor') || '';
  const cursorScope = JSON.stringify([selectedOrg, searchKey, searchModel, from, to, pageSize]);
  const [cursorHistory, setCursorHistory] = useState<{
    scope: string;
    pages: Record<number, string>;
  }>({ scope: '', pages: {} });
  const changeRecordPage = (forward: boolean) => {
    const history = cursorHistory.scope === cursorScope ? cursorHistory.pages : {};
    const targetPage = forward ? page + 1 : history[page - 1] !== undefined ? page - 1 : 1;
    const targetCursor = forward ? recordsData?.next_cursor : history[targetPage] || '';
    setCursorHistory({ scope: cursorScope, pages: { ...history, [page]: cursor } });
    const next = new URLSearchParams(params);
    next.set('page', String(targetPage));
    if (targetCursor) next.set('cursor', targetCursor);
    else next.delete('cursor');
    setParams(next);
  };
  const {
    data: recordsData,
    isLoading: loadingRecords,
    isFetching: fetchingRecords,
    isError: recordsError,
    refetch: refetchRecords,
  } = useQuery({
    queryKey: [
      'admin-usage-records',
      cursor,
      selectedOrg,
      searchKey,
      searchModel,
      from,
      to,
      page,
      pageSize,
    ],
    queryFn: async ({ signal }) => {
      const res = await apiClient.get('/admin/usage', {
        signal,
        params: { ...buildParams(), page, page_size: pageSize, cursor: cursor || undefined },
      });
      return res.data?.data || { items: [], has_more: false };
    },
    enabled: activeTab === 'records' && validRange,
    retry: false,
  });

  const {
    data: aggregateData,
    isLoading: loadingAgg,
    isFetching: fetchingAgg,
    isError: aggError,
  } = useQuery({
    enabled: !recordsOnly && activeTab !== 'organizations' && validRange,
    staleTime: 30000,
    refetchInterval: 30000,
    retry: false,
    queryKey: ['admin-usage-agg', activeTab, selectedOrg, searchKey, searchModel, from, to],
    queryFn: async ({ signal, queryKey }) => {
      const endpoint =
        activeTab === 'weekly'
          ? '/admin/usage/weekly'
          : activeTab === 'monthly'
            ? '/admin/usage/monthly'
            : activeTab === 'records'
              ? '/admin/usage/total'
              : '/admin/usage/daily';
      const res = await apiClient.get(endpoint, {
        signal,
        params: {
          ...buildParams(),
          ...usageRefreshParams(queryClient, queryKey),
        },
      });
      return res.data?.data || { items: [], incomplete: false };
    },
  });

  const { data: orgsData } = useQuery(organizationOptionsQueryOptions());

  const visibleError =
    validRange &&
    ((activeTab === 'records' && recordsError) ||
      (!recordsOnly && activeTab !== 'organizations' && aggError));
  const refreshing =
    (activeTab === 'records' && fetchingRecords) ||
    (!recordsOnly && activeTab !== 'organizations' && fetchingAgg) ||
    (activeTab === 'organizations' && fetchingOrganizations > 0);
  const refreshVisible = () => {
    if (!validRange) return;
    if (activeTab === 'records') void refetchRecords();
    if (!recordsOnly && activeTab !== 'organizations')
      void refreshUsageQueries(queryClient, ['admin-usage-agg', activeTab]);
    if (activeTab === 'organizations')
      void refreshUsageQueries(queryClient, ['admin-organization-daily-usage']);
  };

  const aggItems: any[] =
    validRange && snapshotUsable(aggregateData?.snapshot) ? aggregateData?.items || [] : [];
  const totalRequests = aggItems.reduce((acc, item) => acc + (item.requests || 0), 0);
  const totalTokens = aggItems.reduce((acc, item) => acc + (item.total_tokens || 0), 0);
  const inputTokens = aggItems.reduce((acc, item) => acc + (item.input_tokens || 0), 0);
  const outputTokens = aggItems.reduce((acc, item) => acc + (item.output_tokens || 0), 0);
  const totalErrors = aggItems.reduce((acc, item) => acc + (item.errors || 0), 0);
  const totalCost = aggItems.reduce((acc, item) => acc + (item.cost_amount || 0), 0);

  const recordColumns = [
    {
      title: `${t('usage.col_time', 'Time')} (${timezoneBadge})`,
      dataIndex: 'created_at',
      key: 'created_at',
      width: 170,
      render: (val: string) => (val ? formatDateTime(val) : '-'),
    },
    {
      title: t('usage.col_req_id', 'Request ID'),
      dataIndex: 'request_id',
      key: 'request_id',
      width: 'max-content',
      render: (val: string) => <RequestIDLink value={val} />,
    },
    {
      title: t('usage.col_org_key', 'Org / Key'),
      key: 'org_key',
      width: 180,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, r: any) => (
        <OrgKeyCell
          orgId={r.organization_id}
          keyId={r.api_key_id}
          orgName={r.organization_name}
          keyName={r.api_key_name}
        />
      ),
    },
    {
      title: t('usage.col_key_name', 'Key Name'),
      dataIndex: 'api_key_id',
      key: 'key_name',
      width: 130,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (val: number, record: any) => (
        <ResourceName kind="keys" id={val} name={record.api_key_name} />
      ),
    },
    {
      title: t('usage.col_model', 'Model'),
      dataIndex: 'model',
      key: 'model',
      width: 130,
      ellipsis: true,
      render: (val: string) => <code>{val}</code>,
    },
    {
      title: t('security.col_ip', 'Client IP'),
      dataIndex: 'client_ip',
      key: 'client_ip',
      width: 220,
      render: (val: string) => <code style={{ whiteSpace: 'nowrap' }}>{val || '-'}</code>,
    },
    {
      title: t('usage.col_status', 'Status'),
      dataIndex: 'status_code',
      key: 'status_code',
      width: 85,
      render: (code: number) => <HttpStatusTag value={code} />,
    },
    {
      title: (
        <Tooltip title={t('usage.first_token_ms_hint')}>
          <span>{t('usage.col_first_token_ms', 'Time to First Token')}</span>
        </Tooltip>
      ),
      dataIndex: 'first_token_ms',
      key: 'first_token_ms',
      width: 150,
      align: 'right' as const,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (value: number | null | undefined) => formatFirstTokenLatency(value, formatNumber),
    },
    {
      title: t('usage.col_input_tokens', 'Input Tokens'),
      dataIndex: 'input_tokens',
      key: 'input_tokens',
      width: 105,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_read_tokens', 'Cache Read Tokens'),
      dataIndex: 'cache_read_tokens',
      key: 'cache_read_tokens',
      width: 130,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_write_tokens', 'Cache Write Tokens'),
      dataIndex: 'cache_write_tokens',
      key: 'cache_write_tokens',
      width: 135,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_hit_rate', 'Cache Hit Rate'),
      dataIndex: 'cache_read_tokens',
      key: 'cache_hit_rate',
      width: 115,
      align: 'right' as const,
      render: (_: any, record: any) => {
        const inputTokens = record.input_tokens || 0;
        if (!inputTokens) return '0%';
        return `${(((record.cache_read_tokens || 0) / inputTokens) * 100).toFixed(1)}%`;
      },
    },
    {
      title: t('usage.col_output_tokens', 'Output Tokens'),
      dataIndex: 'output_tokens',
      key: 'output_tokens',
      width: 105,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_tokens', 'Total Tokens'),
      dataIndex: 'total_tokens',
      key: 'total_tokens',
      width: 110,
      align: 'right' as const,
      render: (val: number) => <strong>{formatTokens(val)}</strong>,
    },
    {
      title: t('usage.col_cost', 'Cost (USD)'),
      dataIndex: 'cost_amount',
      key: 'cost_amount',
      width: 115,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
    {
      title: t('usage.col_cache_read_cost', 'Cache Read Cost'),
      dataIndex: 'cache_read_cost_amount',
      key: 'cache_read_cost_amount',
      width: 130,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
    {
      title: t('usage.col_cache_write_cost', 'Cache Write Cost'),
      dataIndex: 'cache_write_cost_amount',
      key: 'cache_write_cost_amount',
      width: 135,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
  ];

  const aggregateColumns = [
    {
      title: t('usage.col_period', 'Period'),
      dataIndex: 'period',
      key: 'period',
      width: 140,
      render: (val: string) => <strong>{val}</strong>,
    },
    {
      title: t('usage.col_requests', 'Requests'),
      dataIndex: 'requests',
      key: 'requests',
      width: 110,
      align: 'right' as const,
      render: (val: number) => formatNumber(val),
    },
    {
      title: t('usage.col_errors', 'Errors'),
      dataIndex: 'errors',
      key: 'errors',
      width: 90,
      align: 'right' as const,
      render: (val: number) => <Tag color={val > 0 ? 'red' : 'green'}>{formatNumber(val)}</Tag>,
    },
    {
      title: t('usage.col_input_tokens', 'Input Tokens'),
      dataIndex: 'input_tokens',
      key: 'input_tokens',
      width: 120,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_read_tokens', 'Cache Read Tokens'),
      dataIndex: 'cache_read_tokens',
      key: 'cache_read_tokens',
      width: 130,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_write_tokens', 'Cache Write Tokens'),
      dataIndex: 'cache_write_tokens',
      key: 'cache_write_tokens',
      width: 135,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_cache_hit_rate', 'Cache Hit Rate'),
      dataIndex: 'cache_read_tokens',
      key: 'cache_hit_rate',
      width: 115,
      align: 'right' as const,
      render: (_: any, record: any) => {
        const inputTokens = record.input_tokens || 0;
        if (!inputTokens) return '0%';
        return `${(((record.cache_read_tokens || 0) / inputTokens) * 100).toFixed(1)}%`;
      },
    },
    {
      title: t('usage.col_output_tokens', 'Output Tokens'),
      dataIndex: 'output_tokens',
      key: 'output_tokens',
      width: 120,
      align: 'right' as const,
      render: (val: number) => formatNumber(val || 0),
    },
    {
      title: t('usage.col_tokens', 'Total Tokens'),
      dataIndex: 'total_tokens',
      key: 'total_tokens',
      width: 130,
      align: 'right' as const,
      render: (val: number) => <strong>{formatTokens(val)}</strong>,
    },
    {
      title: t('usage.col_cost', 'Cost (USD)'),
      dataIndex: 'cost_amount',
      key: 'cost_amount',
      width: 130,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
    {
      title: t('usage.col_cache_read_cost', 'Cache Read Cost'),
      dataIndex: 'cache_read_cost_amount',
      key: 'cache_read_cost_amount',
      width: 130,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
    {
      title: t('usage.col_cache_write_cost', 'Cache Write Cost'),
      dataIndex: 'cache_write_cost_amount',
      key: 'cache_write_cost_amount',
      width: 135,
      align: 'right' as const,
      render: (val: number) => formatCurrency(val, { decimals: 6 }),
    },
  ];

  return (
    <div>
      {aggregateData?.incomplete && (
        <Alert
          type="warning"
          message={t('usage.incomplete_alert', 'Incomplete Telemetry Flag')}
          description={t(
            'usage.incomplete_alert_desc',
            'Some non-blocking queue events were dropped during peak throughput.',
          )}
          showIcon
          style={{ marginBottom: 16 }}
        />
      )}

      <PageHelp note="ux.usage_note" />
      {visibleError && <LoadError retry={refreshVisible} />}
      {!recordsOnly && activeTab !== 'organizations' && (
        <UsageSnapshotStatus snapshot={aggregateData?.snapshot} />
      )}
      {!recordsOnly &&
        activeTab === 'records' &&
        aggregateData &&
        !aggError &&
        snapshotUsable(aggregateData.snapshot) && (
          <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <UsageCountStatistic
                  title={t('usage.agg_requests', 'Aggregated Requests')}
                  value={totalRequests}
                />
              </Card>
            </Col>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <UsageCountStatistic
                  title={t('usage.agg_tokens', 'Aggregated Tokens')}
                  value={totalTokens}
                />
              </Card>
            </Col>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <UsageCountStatistic
                  title={t('usage.input_tokens', 'Input Tokens')}
                  value={inputTokens}
                />
              </Card>
            </Col>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <UsageCountStatistic
                  title={t('usage.output_tokens', 'Output Tokens')}
                  value={outputTokens}
                />
              </Card>
            </Col>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <UsageCountStatistic
                  title={t('usage.agg_errors', 'Aggregated Errors')}
                  value={totalErrors}
                />
              </Card>
            </Col>
            <Col xs={12} sm={8} lg={8} xl={4}>
              <Card style={{ height: '100%' }}>
                <Statistic
                  title={t('usage.agg_cost', 'Total Cost')}
                  value={totalCost}
                  formatter={v => formatCurrency(Number(v), { decimals: 2 })}
                />
              </Card>
            </Col>
          </Row>
        )}

      <Card>
        <ConsoleToolbar actions={<RefreshButton loading={refreshing} onClick={refreshVisible} />}>
          <div className="console-toolbar-group">
            <Select
              allowClear
              placeholder={t('usage.filter_org', 'Filter Organization')}
              aria-label={t('usage.filter_org', 'Filter Organization')}
              style={{ width: 140 }}
              value={selectedOrg}
              onChange={v => {
                setSelectedOrg(v);
              }}
              options={orgsData?.map((o: any) => ({ label: o.name, value: o.id }))}
            />
            <KeySelect
              value={Number(selectedKey) || undefined}
              organizationId={selectedOrg}
              placeholder={t('usage.filter_key_name', 'Filter Key')}
              style={{ width: 140 }}
              onChange={value => setSelectedKey(value ? String(value) : '')}
            />
            <Input
              placeholder={t('usage.filter_model', 'Model ID (e.g. gpt-4o)')}
              aria-label={t('usage.filter_model', 'Model ID (e.g. gpt-4o)')}
              style={{ width: 150 }}
              value={modelFilter}
              onChange={e => setModelFilter(e.target.value)}
              allowClear
            />
          </div>
          <div className="console-toolbar-group">
            <DatePicker
              aria-label={t('usage.from_date')}
              placeholder={t('usage.from_date')}
              status={validRange ? undefined : 'error'}
              format="YYYY-MM-DD"
              value={dayjs(from).isValid() ? dayjs(from) : null}
              onChange={date => updateFilter('from', date?.format('YYYY-MM-DD') || '')}
              style={{ width: 150 }}
            />
            <DatePicker
              aria-label={t('usage.to_date')}
              placeholder={t('usage.to_date')}
              status={validRange ? undefined : 'error'}
              format="YYYY-MM-DD"
              value={dayjs(to).isValid() ? dayjs(to) : null}
              onChange={date => updateFilter('to', date?.format('YYYY-MM-DD') || '')}
              style={{ width: 150 }}
            />
            <Button onClick={() => setParams({ view: activeTab })}>{t('ux.clear')}</Button>
          </div>
        </ConsoleToolbar>
        <div style={{ color: 'var(--gray)', fontSize: 12, marginBottom: 16 }}>
          {t('overview.time_basis')}
        </div>
        {!validRange && activeTab !== 'organizations' && (
          <Alert
            type="warning"
            showIcon
            message={t('usage.invalid_user_range')}
            style={{ marginBottom: 16 }}
          />
        )}
        <Tabs
          activeKey={activeTab}
          onChange={k => updateFilter('view', k)}
          items={[
            {
              key: 'organizations',
              label: t('usage.users_daily_tab'),
              children: (
                <DailyOrganizationUsage
                  active={activeTab === 'organizations'}
                  filters={{
                    organization_id: selectedOrg,
                    key_id: Number(searchKey) || undefined,
                    model: searchModel.trim() || undefined,
                    from,
                    to,
                  }}
                  page={page}
                  pageSize={pageSize}
                  onPageChange={(p, ps) => {
                    const next = new URLSearchParams(params);
                    next.set('page', String(ps === pageSize ? p : 1));
                    next.set('page_size', String(ps));
                    setParams(next);
                  }}
                />
              ),
            },
            {
              key: 'records',
              label: t('usage.records_tab', 'Request Ledger'),
              children: (
                <Table
                  rowKey="id"
                  dataSource={validRange ? recordsData?.items || [] : []}
                  columns={[
                    ...recordColumns.filter(column =>
                      [
                        'created_at',
                        'key_name',
                        'model',
                        'status_code',
                        'first_token_ms',
                        'cache_read_tokens',
                        'cache_write_tokens',
                        'cache_hit_rate',
                        'total_tokens',
                        'cost_amount',
                        'cache_read_cost_amount',
                        'cache_write_cost_amount',
                      ].includes(column.key),
                    ),
                    {
                      title: t('common.actions'),
                      key: 'actions',
                      width: 80,
                      fixed: isWideScreen ? ('right' as const) : undefined,
                      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
                      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
                      render: (_: any, record: any) => (
                        <TableAction
                          size="small"
                          style={{ whiteSpace: 'nowrap' }}
                          onClick={() => setDetail(record)}
                        >
                          {t('ux.details')}
                        </TableAction>
                      ),
                    },
                  ]}
                  loading={loadingRecords}
                  scroll={{ x: 1500 }}
                  pagination={false}
                  footer={() => (
                    <CursorPagination
                      page={page}
                      hasNext={!!recordsData?.has_more}
                      loading={fetchingRecords}
                      onPrevious={() => changeRecordPage(false)}
                      onNext={() => changeRecordPage(true)}
                    />
                  )}
                />
              ),
            },
            {
              key: 'daily',
              label: t('usage.daily_tab', 'Daily Aggregation'),
              children: (
                <Table
                  rowKey="period"
                  dataSource={aggItems}
                  columns={aggregateColumns}
                  loading={loadingAgg}
                  scroll={{ x: 1500 }}
                  pagination={{ pageSize: 30 }}
                />
              ),
            },
            {
              key: 'weekly',
              label: t('usage.weekly_tab', 'Weekly Aggregation'),
              children: (
                <Table
                  rowKey="period"
                  dataSource={aggItems}
                  columns={aggregateColumns}
                  loading={loadingAgg}
                  scroll={{ x: 1500 }}
                  pagination={{ pageSize: 15 }}
                />
              ),
            },
            {
              key: 'monthly',
              label: t('usage.monthly_tab', 'Monthly Aggregation'),
              children: (
                <Table
                  rowKey="period"
                  dataSource={aggItems}
                  columns={aggregateColumns}
                  loading={loadingAgg}
                  scroll={{ x: 1500 }}
                  pagination={{ pageSize: 12 }}
                />
              ),
            },
          ].filter(item => !recordsOnly || item.key === 'records')}
        />
      </Card>
      <Drawer
        title={t('ux.details')}
        width={640}
        open={!!detail}
        onClose={() => setDetail(null)}
        destroyOnHidden
      >
        <PageHelp note="ux.usage_note" />
        {detail && (
          <RecordDetails>
            {recordColumns.map((column: any) => (
              <Descriptions.Item key={column.key} label={column.title}>
                {column.render
                  ? column.render(detail[column.dataIndex], detail)
                  : detail[column.dataIndex]}
              </Descriptions.Item>
            ))}
          </RecordDetails>
        )}
      </Drawer>
    </div>
  );
};
