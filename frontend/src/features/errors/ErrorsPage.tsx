import React from 'react';
import { Alert, Card, Table, Space, Button, Tooltip, Select, Typography } from 'antd';
import { CursorPagination } from '../common/CursorPagination';
import { useCursorPagination } from '../common/useCursorPagination';
import { HttpStatusTag } from '../common/HttpStatusTag';
import { InfoCircleOutlined } from '@ant-design/icons';
import { SearchInput } from '../common/SearchInput';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery } from '@tanstack/react-query';
import { useI18n } from '../../i18n';
import { RequestIDLink } from '../requestlogs/RequestIDLink';
import { OrgKeyCell } from '../common/OrgKeyCell';
import { useSearchParams } from 'react-router-dom';
import { LoadError } from '../common/ConsoleUX';
import { validRequestID } from '../common/ux';
import { useDebouncedValue } from '../common/useDebouncedValue';
import { errorDiagnosis } from '../requestlogs/diagnostics';
import { FailureDetails } from './FailureDetails';
import { ERROR_WINDOW_MINUTES, errorQueryOptions, errorWindowMinutes } from './errorQuery';

export const ErrorsPage: React.FC = () => {
  const { t, formatDateTime, formatNumber, timezoneBadge } = useI18n();
  const [params, setParams] = useSearchParams();
  const organizationID = params.get('organization_id');
  const keyID = params.get('key_id');
  const filterCode = params.get('error_code') || '';
  const searchCode = useDebouncedValue(filterCode);
  const windowMinutes = errorWindowMinutes(params.get('window_minutes'));
  const pageSize = [10, 20, 50, 100].includes(Number(params.get('page_size')))
    ? Number(params.get('page_size'))
    : 20;
  const [refreshVersion, setRefreshVersion] = React.useState(0);
  const scope = JSON.stringify([
    searchCode,
    organizationID,
    keyID,
    pageSize,
    windowMinutes,
    refreshVersion,
  ]);
  const [savedWindow, setSavedWindow] = React.useState(() => ({
    scope,
    to: new Date().toISOString(),
  }));
  const timeWindow =
    savedWindow.scope === scope ? savedWindow : { scope, to: new Date().toISOString() };
  if (savedWindow.scope !== scope) setSavedWindow(timeWindow);
  const pagination = useCursorPagination(scope);
  const currentCursor = pagination.cursor;
  const refresh = () => setRefreshVersion(value => value + 1);

  const {
    data: errorLogs,
    isLoading,
    isFetching,
    isError,
    error,
  } = useQuery(
    errorQueryOptions({
      limit: pageSize,
      window_minutes: windowMinutes,
      to: timeWindow.to,
      cursor: currentCursor || undefined,
      error_code: searchCode || undefined,
      organization_id: organizationID || undefined,
      key_id: keyID || undefined,
    }),
  );

  const columns = [
    {
      title: `${t('errors.col_time', 'Timestamp')} (${timezoneBadge})`,
      dataIndex: 'time',
      key: 'time',
      width: 165,
      render: (val: string) => <span>{val ? formatDateTime(val) : '-'}</span>,
    },
    {
      title: t('errors.col_req_id', 'Request ID'),
      dataIndex: 'request_id',
      key: 'request_id',
      width: 180,
      render: (id: string) => <RequestIDLink value={id} />,
    },
    {
      title: t('errors.col_org_key', 'Org / Key'),
      key: 'org_key',
      width: 160,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, record: any) => (
        <OrgKeyCell
          orgId={record.organization_id || record.org_id}
          keyId={record.api_key_id || record.key_id}
        />
      ),
    },
    {
      title: t('errors.col_http_status', 'HTTP Status'),
      dataIndex: 'http_status',
      key: 'http_status',
      width: 85,
      align: 'center' as const,
      render: (status: number) => <HttpStatusTag value={status} />,
    },
    {
      title: t('ux.failure_context'),
      key: 'context',
      width: 190,
      render: (_: unknown, record: any) => (
        <div style={{ overflowWrap: 'anywhere' }}>
          <div>{record.model || '-'}</div>
          <code>{record.endpoint || '-'}</code>
        </div>
      ),
    },
    {
      title: t('errors.col_error_code', 'Error Code'),
      dataIndex: 'error_code',
      key: 'error_code',
      width: 240,
      render: (code: string) => (
        <div style={{ overflowWrap: 'anywhere' }}>
          <div>{t(`ux.diagnosis_${errorDiagnosis(code)}`)}</div>
          <code>{code}</code>
        </div>
      ),
    },
    {
      title: t('errors.col_latency', 'Latency'),
      dataIndex: 'duration_ms',
      key: 'duration_ms',
      width: 85,
      align: 'right' as const,
      render: (ms: number) => <span>{formatNumber(ms ?? 0)}ms</span>,
    },
  ];

  return (
    <Card
      title={
        <Space align="center" size={8}>
          <span>{t('errors.title', 'Gateway Sanitized Error Logs')}</span>
          <Tooltip
            title={t(
              'errors.telemetry_desc',
              'Error logs record only timestamps, request IDs, safe error codes, HTTP statuses, and duration. Upstream raw error bodies and client prompts are never retained.',
            )}
          >
            <InfoCircleOutlined style={{ color: '#8c8c8c', cursor: 'pointer' }} />
          </Tooltip>
        </Space>
      }
      extra={
        <Space wrap>
          <Select
            aria-label={t('errors.time_range')}
            value={windowMinutes}
            style={{ width: 150 }}
            options={ERROR_WINDOW_MINUTES.map(minutes => ({
              value: minutes,
              label: t(minutes < 60 ? 'errors.window_minutes' : 'errors.window_hours').replace(
                '{count}',
                String(minutes < 60 ? minutes : minutes / 60),
              ),
            }))}
            onChange={value => {
              const next = new URLSearchParams(params);
              next.set('window_minutes', String(value));
              setParams(next, { replace: true });
            }}
          />
          <SearchInput
            placeholder={t('errors.filter_code', 'Filter by Error Code')}
            value={filterCode}
            onChange={e => {
              const next = new URLSearchParams(params);
              if (e.target.value) next.set('error_code', e.target.value);
              else next.delete('error_code');
              setParams(next, { replace: true });
            }}
            style={{ width: 220 }}
            allowClear
          />
          <RefreshButton loading={isFetching} onClick={refresh} />
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">
        {t('errors.window_hint')} ·{' '}
        {formatDateTime(new Date(Date.parse(timeWindow.to) - windowMinutes * 60_000).toISOString())}
        {' – '}
        {formatDateTime(timeWindow.to)} ({timezoneBadge})
      </Typography.Paragraph>
      {isError && (
        <>
          <LoadError retry={refresh} />
          {(error as any)?.response?.status === 504 && (
            <Alert
              type="warning"
              message={t('errors.query_timeout')}
              style={{ marginBottom: 16 }}
            />
          )}
        </>
      )}
      {(organizationID || keyID || filterCode || windowMinutes !== 5) && (
        <Space wrap style={{ marginBottom: 16 }}>
          {(organizationID || keyID) && <OrgKeyCell orgId={organizationID} keyId={keyID} />}
          <Button onClick={() => setParams({})}>{t('ux.clear')}</Button>
        </Space>
      )}
      <Table
        tableLayout="fixed"
        rowKey={(row: any) => `${row.time}-${row.request_id}-${row.error_code}`}
        dataSource={errorLogs?.items || []}
        columns={columns}
        loading={isLoading}
        locale={{ emptyText: t('errors.empty_window') }}
        scroll={{ x: 1250 }}
        expandable={{
          rowExpandable: row => validRequestID(row.request_id || ''),
          expandedRowRender: row => <FailureDetails requestID={row.request_id} />,
          columnTitle: t('ux.details'),
          columnWidth: 90,
          expandIcon: ({ expanded, onExpand, record, expandable }) =>
            expandable ? (
              <Button
                type="link"
                size="small"
                aria-expanded={expanded}
                onClick={event => onExpand(record, event)}
              >
                {t(expanded ? 'ux.collapse_details' : 'ux.details')}
              </Button>
            ) : null,
        }}
        pagination={false}
      />
      <CursorPagination
        page={pagination.page}
        hasNext={!!errorLogs?.next_cursor}
        loading={isFetching || searchCode !== filterCode}
        onPrevious={pagination.previous}
        onNext={() => pagination.next(errorLogs?.next_cursor)}
        pageNumbers={Array.from({ length: Math.min(5, pagination.history.length) }, (_, index) => {
          const firstPage = Math.max(
            1,
            Math.min(pagination.page - 4, pagination.history.length - 4),
          );
          return firstPage + index;
        })}
        onPageChange={pagination.goToPage}
      >
        <Select
          aria-label={t('common.page_size')}
          value={pageSize}
          options={[10, 20, 50, 100].map(value => ({
            value,
            label: t('common.rows_per_page', '{count} / page').replace('{count}', String(value)),
          }))}
          onChange={value => {
            const next = new URLSearchParams(params);
            next.set('page_size', String(value));
            setParams(next, { replace: true });
          }}
        />
      </CursorPagination>
    </Card>
  );
};
