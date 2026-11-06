import { ResourceName } from '../common/ResourceName';
import { compactKey } from '../common/display';
import React, { useEffect, useState } from 'react';
import { Alert, Button, Card, Empty, Table, Tag, Typography, Collapse, Timeline } from 'antd';
import { CursorPagination } from '../common/CursorPagination';
import { SearchInput } from '../common/SearchInput';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery } from '@tanstack/react-query';
import { Link, useLocation, useNavigate, useOutletContext } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { stepKey, validRequestID } from '../common/ux';
import { LoadError, PageHelp } from '../common/ConsoleUX';
import { TraceDiagnosis, TraceMetadata } from './TraceDiagnosis';
import { entryDiagnosis, logFields, type RequestLogEntry } from './diagnostics';

const { Text } = Typography;

export const RequestLogsPage: React.FC = () => {
  const location = useLocation();
  const navigate = useNavigate();
  const { t, formatDateTime } = useI18n();
  const { user } = useOutletContext<{ user?: { role: string } }>();
  const initialID = new URLSearchParams(location.search).get('request_id') || '';
  const [requestID, setRequestID] = useState(initialID);
  const [searchedID, setSearchedID] = useState(initialID);
  const [page, setPage] = useState(1);
  const [invalid, setInvalid] = useState(false);
  const pageSize = 100;

  useEffect(() => {
    const nextID = new URLSearchParams(location.search).get('request_id') || '';
    setRequestID(nextID);
    setSearchedID(nextID);
    setPage(1);
    setInvalid(false);
  }, [location.search]);

  const logs = useQuery({
    queryKey: ['admin-request-logs', searchedID, page],
    queryFn: async () => {
      const response = await apiClient.get('/admin/request-logs', {
        params: { request_id: searchedID, limit: pageSize, offset: (page - 1) * pageSize },
      });
      return response.data?.data || { items: [], has_more: false };
    },
    staleTime: 30000,
    retry: false,
    enabled: validRequestID(searchedID),
  });

  const search = (value = requestID) => {
    const id = value.trim();
    setInvalid(!!id && !validRequestID(id));
    if (id && !validRequestID(id)) return;
    if (!id) {
      navigate('/admin/request-logs');
      setSearchedID('');
      setPage(1);
      return;
    }
    setPage(1);
    navigate(`/admin/request-logs?request_id=${encodeURIComponent(id)}`);
  };

  const columns = [
    {
      title: t('request_logs.time', 'Time'),
      dataIndex: 'created_at',
      key: 'created_at',
      width: 190,
      render: (value: string) => (
        <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(value)}</span>
      ),
    },
    {
      title: t('request_logs.level', 'Level'),
      dataIndex: 'level',
      key: 'level',
      width: 100,
      render: (value: string) => (
        <Tag color={value === 'ERROR' ? 'red' : value === 'WARN' ? 'orange' : 'blue'}>{value}</Tag>
      ),
    },
    {
      title: t('request_logs.event', 'Event'),
      dataIndex: 'event',
      key: 'event',
      width: 220,
      render: (value: string) => (
        <Text code style={{ whiteSpace: 'nowrap' }}>
          {value}
        </Text>
      ),
    },
    {
      title: t('request_logs.message', 'Details'),
      dataIndex: 'message',
      key: 'message',
      render: (value: string) => (
        <Text style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{value}</Text>
      ),
    },
    {
      title: t('request_logs.ip', 'Client IP'),
      dataIndex: 'ip',
      key: 'ip',
      width: 180,
      render: (value: string) => (
        <Text code style={{ whiteSpace: 'nowrap' }}>
          {value || '-'}
        </Text>
      ),
    },
    {
      title: t('request_logs.organization', 'Organization'),
      dataIndex: 'organization_id',
      key: 'organization_id',
      width: 125,
      render: (value: number) => <ResourceName kind="organizations" id={value} />,
    },
    {
      title: t('request_logs.api_key', 'API Key'),
      dataIndex: 'api_key_masked',
      key: 'api_key_masked',
      width: 155,
      render: (value: string) => (value ? compactKey({ masked_key: value }) : '-'),
    },
  ];

  return (
    <Card
      title={t('request_logs.title', 'Request Trace & Server Logs')}
      extra={
        <RefreshButton
          onClick={() => logs.refetch()}
          disabled={!validRequestID(searchedID)}
          loading={logs.isFetching}
        />
      }
    >
      <PageHelp note="ux.trace_note" />
      {(invalid || (searchedID && !validRequestID(searchedID))) && (
        <Alert type="warning" message={t('ux.invalid_id')} />
      )}
      {logs.isError && <LoadError retry={() => logs.refetch()} />}
      <div
        style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 16, maxWidth: 640 }}
      >
        <SearchInput
          aria-label={t('request_logs.request_id', 'Request ID')}
          placeholder={t('request_logs.placeholder', 'Enter a request ID')}
          value={requestID}
          onChange={event => setRequestID(event.target.value)}
          onPressEnter={() => search()}
          allowClear
          style={{ flex: 1, minWidth: 0 }}
        />
        <Button type="primary" onClick={() => search()}>
          {t('common.search', 'Search')}
        </Button>
      </div>

      {searchedID && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message={
            <span
              style={{
                display: 'block',
                maxWidth: '100%',
                overflowX: 'auto',
                whiteSpace: 'nowrap',
              }}
            >
              {t('request_logs.request_id', 'Request ID')}: {searchedID}
            </span>
          }
          description={t(
            'request_logs.privacy',
            'Request logs contain sanitized operational metadata. Credentials and request contents are not stored.',
          )}
        />
      )}

      {!searchedID ? (
        <Empty
          description={t(
            'request_logs.empty_prompt',
            'Search by request ID to view its server log chain.',
          )}
        />
      ) : validRequestID(searchedID) ? (
        <>
          {(logs.data || !logs.isError) && (
            <>
              {(page > 1 || logs.data?.has_more) && (
                <Alert type="warning" message={t('ux.incomplete')} style={{ marginBottom: 16 }} />
              )}
              {!logs.isLoading && !!logs.data?.items?.length && (
                <TraceDiagnosis
                  rows={logs.data?.items || []}
                  incomplete={page > 1 || !!logs.data?.has_more}
                />
              )}
              <Card
                size="small"
                title={t('ux.trace_summary')}
                loading={logs.isLoading}
                style={{ marginBottom: 16 }}
              >
                <Timeline
                  items={(logs.data?.items || []).map(
                    (row: RequestLogEntry, index: number, rows: RequestLogEntry[]) => {
                      const diagnosis = entryDiagnosis(row);
                      const delta =
                        index > 0
                          ? Date.parse(row.created_at) - Date.parse(rows[index - 1].created_at)
                          : undefined;
                      return {
                        color:
                          diagnosis || row.level === 'ERROR'
                            ? 'red'
                            : row.level === 'WARN'
                              ? 'orange'
                              : 'blue',
                        children: (
                          <div>
                            <strong>
                              {stepKey(row.event) ? t(`ux.${stepKey(row.event)}`) : row.event}
                            </strong>
                            <span style={{ marginLeft: 12, color: '#737373' }}>
                              {formatDateTime(row.created_at)}
                            </span>
                            {delta !== undefined && Number.isFinite(delta) && delta >= 0 && (
                              <Tag style={{ marginLeft: 12 }}>
                                {t('ux.elapsed_step').replace('{ms}', String(delta))}
                              </Tag>
                            )}
                            {diagnosis && (
                              <div style={{ color: 'var(--error)', marginTop: 4 }}>
                                {t(`ux.diagnosis_${diagnosis}`)}
                              </div>
                            )}
                            <TraceMetadata fields={logFields(row.message)} />
                            <Text type="secondary" style={{ overflowWrap: 'anywhere' }}>
                              {row.message}
                            </Text>
                          </div>
                        ),
                      };
                    },
                  )}
                />
                {!logs.isLoading && !logs.data?.items?.length && (
                  <Empty
                    description={
                      <>
                        <div>{t('request_logs.no_entries')}</div>
                        <Text type="secondary">{t('qa.trace_empty_help')}</Text>
                      </>
                    }
                  />
                )}
              </Card>
              {!!logs.data?.items?.length && (
                <>
                  <p>
                    {t('ux.inspect')}:{' '}
                    <Link to="/admin/organizations">{t('nav.organizations')}</Link> ·{' '}
                    <Link to="/admin/keys">{t('nav.keys')}</Link> ·{' '}
                    <Link to="/admin/rate-limits">{t('nav.rate_limits')}</Link>
                    {user?.role === 'super_admin' && (
                      <>
                        {' '}
                        · <Link to="/admin/providers">{t('nav.providers')}</Link> ·{' '}
                        <Link to="/admin/providers">{t('nav.providers')}</Link>
                      </>
                    )}
                  </p>
                  <Collapse
                    items={[
                      {
                        key: 'raw',
                        label: t('ux.raw_logs'),
                        children: (
                          <Table
                            rowKey="id"
                            size="small"
                            scroll={{ x: 1200 }}
                            dataSource={logs.data?.items || []}
                            columns={columns}
                            loading={logs.isLoading}
                            pagination={false}
                            locale={{
                              emptyText: t(
                                'request_logs.no_entries',
                                'No log entries found for this request ID.',
                              ),
                            }}
                          />
                        ),
                      },
                    ]}
                  />
                </>
              )}
            </>
          )}
          {(page > 1 || logs.data?.has_more) && (
            <CursorPagination
              page={page}
              hasNext={!logs.isError && !!logs.data?.has_more}
              loading={logs.isFetching}
              onPrevious={() => setPage(current => Math.max(1, current - 1))}
              onNext={() => setPage(current => current + 1)}
            />
          )}
        </>
      ) : null}
    </Card>
  );
};
