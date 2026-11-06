import { Alert, Card, Col, Input, Row, Statistic, Table } from 'antd';
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';
import { useDebouncedValue } from '../common/useDebouncedValue';
import { validUserUsageRange } from './userUsageFilters';
import { OrganizationTokenRanking } from './OrganizationTokenRanking';
import { UsageCountStatistic } from './UsageCountStatistic';
import { UsageSnapshotStatus, snapshotUsable, type UsageSnapshot } from './UsageSnapshotStatus';
import { usageRefreshParams } from './usageQueryCache';
import type { KeyTokenRank, ModelTokenRank, OrganizationTokenRank } from './tokenRanking';

interface UsageTotals {
  requests: number;
  errors: number;
  input_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cache_hit_rate: number;
  cache_read_cost_amount: number;
  cache_write_cost_amount: number;
  output_tokens: number;
  total_tokens: number;
  cost_amount: number;
}

interface OrganizationUsageRow extends UsageTotals {
  period: string;
  organization_id: number;
  organization_name: string;
  key_count: number;
}

export function DailyOrganizationUsage({
  active,
  filters,
  page,
  pageSize,
  onPageChange,
}: {
  active: boolean;
  filters: { organization_id?: number; key_id?: number; model?: string; from: string; to: string };
  page: number;
  pageSize: number;
  onPageChange: (page: number, pageSize: number) => void;
}) {
  const { t, formatNumber, formatCurrency } = useI18n();
  const queryClient = useQueryClient();
  const [rankingTab, setRankingTab] = useState('organizations');
  const [params, setParams] = useSearchParams();
  const search = params.get('search') || '';
  const debouncedSearch = useDebouncedValue(search);
  const validRange = validUserUsageRange(filters.from, filters.to);
  const scope = JSON.stringify([filters, debouncedSearch]);
  const [version, setVersion] = useState({ scope: '', id: '' });
  const snapshotId = version.scope === scope ? version.id : '';
  const query = useQuery({
    queryKey: [
      'admin-organization-daily-usage',
      filters,
      debouncedSearch,
      page,
      pageSize,
      page > 1 ? snapshotId : '',
    ],
    staleTime: 30000,
    refetchInterval: 30000,
    retry: false,
    enabled: active && validRange,
    queryFn: async ({ signal, queryKey }) => {
      const response = await apiClient.get('/admin/usage/organizations/daily', {
        signal,
        params: {
          ...filters,
          search: debouncedSearch.trim() || undefined,
          page,
          page_size: pageSize,
          snapshot_id: page > 1 ? snapshotId || undefined : undefined,
          ...usageRefreshParams(queryClient, queryKey),
        },
      });
      return response.data.data as {
        snapshot?: UsageSnapshot;
        items: OrganizationUsageRow[];
        total: number;
        summary: UsageTotals;
        top_organizations?: OrganizationTokenRank[];
        top_keys?: KeyTokenRank[];
        top_models?: ModelTokenRank[];
      };
    },
  });
  useEffect(() => {
    const snapshot = query.data?.snapshot;
    if (snapshot?.status === 'replaced') {
      queryClient.removeQueries({ queryKey: ['admin-organization-daily-usage'], type: 'inactive' });
      const next = new URLSearchParams(params);
      next.delete('page');
      setParams(next, { replace: true });
      setVersion({ scope, id: '' });
    } else if (snapshot?.snapshot_id && snapshotUsable(snapshot)) {
      setVersion(previous =>
        previous.scope === scope && previous.id === snapshot.snapshot_id
          ? previous
          : { scope, id: snapshot.snapshot_id! },
      );
    }
  }, [query.data?.snapshot, scope, params, setParams, queryClient]);
  const usable = snapshotUsable(query.data?.snapshot);
  const columns = [
    { title: t('usage.col_date'), dataIndex: 'period', width: 120 },
    ...(
      [
        'total_tokens',
        'input_tokens',
        'cache_read_tokens',
        'cache_write_tokens',
        'cache_hit_rate',
        'output_tokens',
        'requests',
        'key_count',
        'errors',
      ] as const
    ).map(key => ({
      title: t(`usage.${key === 'key_count' ? 'col_keys' : key}`),
      dataIndex: key,
      width: key === 'cache_hit_rate' ? 110 : 130,
      align: 'right' as const,
      render: (value: number) =>
        key === 'cache_hit_rate'
          ? `${(Number(value || 0) * 100).toFixed(1)}%`
          : formatNumber(value),
    })),
    {
      title: t('usage.amount'),
      dataIndex: 'cost_amount',
      width: 140,
      align: 'right' as const,
      render: (value: number) => formatCurrency(value, { decimals: 6 }),
    },
    {
      title: t('usage.col_org'),
      dataIndex: 'organization_name',
      width: 160,
      render: (name: string, row: OrganizationUsageRow) => name || `#${row.organization_id}`,
    },
  ];
  return (
    <>
      <Input.Search
        value={search}
        allowClear
        maxLength={320}
        aria-label={t('usage.filter_user')}
        placeholder={t('usage.filter_user')}
        onChange={event => {
          const next = new URLSearchParams(params);
          if (event.target.value) next.set('search', event.target.value);
          else next.delete('search');
          next.delete('page');
          setParams(next, { replace: true });
        }}
        style={{ width: 300, maxWidth: '100%', marginBottom: 16 }}
      />
      {!validRange && (
        <Alert
          type="warning"
          showIcon
          message={t('usage.invalid_user_range')}
          style={{ marginBottom: 16 }}
        />
      )}
      <UsageSnapshotStatus snapshot={query.data?.snapshot} note={t('usage.users_note')} />
      {validRange && query.isError && <LoadError retry={() => void query.refetch()} />}
      {validRange && query.data && !query.isError && usable && (
        <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
          {(
            [
              'requests',
              'input_tokens',
              'cache_read_tokens',
              'cache_write_tokens',
              'cache_hit_rate',
              'output_tokens',
              'total_tokens',
              'errors',
              'cost_amount',
            ] as const
          ).map(key => (
            <Col key={key} xs={12} md={8} xl={4}>
              <Card size="small">
                {key === 'cost_amount' ? (
                  <Statistic
                    title={t('usage.agg_cost')}
                    value={query.data.summary[key]}
                    formatter={value => formatCurrency(Number(value), { decimals: 2 })}
                  />
                ) : key === 'cache_hit_rate' ? (
                  <Statistic
                    title={t(`usage.${key}`)}
                    value={query.data.summary[key]}
                    formatter={value => `${(Number(value || 0) * 100).toFixed(1)}%`}
                  />
                ) : (
                  <UsageCountStatistic title={t(`usage.${key}`)} value={query.data.summary[key]} />
                )}
              </Card>
            </Col>
          ))}
        </Row>
      )}
      {validRange && query.data && !query.isError && usable && (
        <OrganizationTokenRanking
          activeTab={rankingTab}
          onTabChange={setRankingTab}
          items={query.data.top_organizations}
          keys={query.data.top_keys}
          models={query.data.top_models}
          totalTokens={query.data.summary.total_tokens}
          from={filters.from}
          to={filters.to}
        />
      )}
      <Table<OrganizationUsageRow>
        rowKey={row => `${row.period}:${row.organization_id}`}
        columns={columns}
        dataSource={validRange && !query.isError && usable ? query.data?.items || [] : []}
        loading={query.isFetching}
        scroll={{ x: 1700 }}
        pagination={{
          current: page,
          pageSize,
          total: validRange && !query.isError && usable ? query.data?.total || 0 : 0,
          showSizeChanger: true,
          pageSizeOptions: [10, 20, 50, 100],
          onChange: onPageChange,
        }}
      />
    </>
  );
}
