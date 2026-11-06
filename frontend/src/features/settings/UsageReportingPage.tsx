import React from 'react';
import { Alert, Card, Descriptions, Modal, Space, Switch, Tag, Typography, message } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { formatDateTime } from '../../i18n/formatters';
import { LoadError } from '../common/ConsoleUX';
import { INSTALLATION_REPORT_FIELDS } from './installationTelemetry';

interface ReportingStatus {
  state: 'disabled' | 'enabled' | 'forced_disabled' | 'unavailable';
  enabled: boolean;
  effective_enabled: boolean;
  disabled_reason?: string;
  receiver: string;
  timezone: string;
  installation_id?: string;
  last_success_at?: string;
  next_scheduled_at?: string;
  last_failure_at?: string;
  last_error?: string;
  pending_count: number;
  dropped_count: number;
  fields: string[];
}
const path = '/admin/usage-reporting';
const queryKey = ['usage-reporting'];

export const UsageReportingPage: React.FC = () => {
  const { t } = useI18n();
  const tr = (key: string) => t(`usage_reporting.${key}`);
  const client = useQueryClient();
  const settings = useQuery({
    queryKey,
    queryFn: async ({ signal }) =>
      (await apiClient.get(path, { signal, skipGlobalErrorMessage: true })).data
        .data as ReportingStatus,
    refetchInterval: 15000,
  });
  const failure = () => {
    void message.error(tr('failed'));
  };
  const save = useMutation({
    mutationFn: async (enabled: boolean) =>
      (await apiClient.put(path, { enabled }, { skipGlobalErrorMessage: true })).data
        .data as ReportingStatus,
    onMutate: async () => {
      await client.cancelQueries({ queryKey });
    },
    onSuccess: async data => {
      client.setQueryData(queryKey, data);
      await client.invalidateQueries({ queryKey });
      void message.success(tr('saved'));
    },
    onError: () => {
      failure();
      void client.invalidateQueries({ queryKey });
    },
  });
  const data = settings.data;
  const busy = save.isPending;
  const change = (enabled: boolean) => {
    if (enabled) {
      Modal.confirm({
        title: tr('enable_title'),
        content: (
          <Space direction="vertical">
            <span>{tr('notice')}</span>
            <span>
              {tr('receiver')}: {data?.receiver}
            </span>
            <span>{tr('fields_help')}</span>
            <span>{tr('retention')}</span>
            <span>{tr('close_help')}</span>
          </Space>
        ),
        okText: tr('enable_confirm'),
        onOk: () => save.mutateAsync(true).then(() => undefined),
      });
    } else {
      save.mutate(false);
    }
  };
  return (
    <Card title={tr('title')} loading={settings.isLoading}>
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        <Alert type="info" showIcon message={tr('notice')} />
        {settings.isError && (
          <LoadError
            retry={() => {
              void settings.refetch();
            }}
          />
        )}
        {data && (
          <>
            <Space wrap>
              <Switch
                aria-label={tr('switch')}
                checked={data.enabled}
                loading={busy}
                disabled={
                  settings.isError ||
                  busy ||
                  data.state === 'forced_disabled' ||
                  (!data.enabled && data.state === 'unavailable')
                }
                onChange={change}
              />
              <Typography.Text>{tr('switch')}</Typography.Text>
              <Tag color={data.effective_enabled ? 'success' : 'default'}>{tr(data.state)}</Tag>
            </Space>
            {data.disabled_reason && (
              <Alert type="warning" message={tr(`reason_${data.disabled_reason}`)} showIcon />
            )}
            <Descriptions
              column={{ xs: 1, sm: 2 }}
              bordered
              size="small"
              items={[
                {
                  key: 'receiver',
                  label: tr('receiver'),
                  children: data.receiver || tr('missing'),
                },
                { key: 'calendar', label: tr('calendar'), children: `${data.timezone} (UTC+0)` },
                {
                  key: 'last',
                  label: tr('last_success'),
                  children: data.last_success_at
                    ? formatDateTime(data.last_success_at, 'UTC')
                    : tr('none'),
                },
                {
                  key: 'next',
                  label: tr('next'),
                  children: data.next_scheduled_at
                    ? formatDateTime(data.next_scheduled_at, 'UTC')
                    : tr('none'),
                },
                { key: 'pending', label: tr('pending'), children: data.pending_count },
                { key: 'dropped', label: tr('dropped'), children: data.dropped_count },
                {
                  key: 'error',
                  label: tr('last_error'),
                  span: 2,
                  children: data.last_error
                    ? `${data.last_failure_at ? formatDateTime(data.last_failure_at, 'UTC') : ''} ${tr(`error_${data.last_error}`)}`
                    : tr('none'),
                },
              ]}
            />
            <Typography.Paragraph>{tr('frequency')}</Typography.Paragraph>
            <div>
              <Typography.Text strong>{tr('fields')}</Typography.Text>
              <Typography.Paragraph>{tr('fields_help')}</Typography.Paragraph>
              <Typography.Text code style={{ overflowWrap: 'anywhere' }}>
                {INSTALLATION_REPORT_FIELDS.join(', ')}
              </Typography.Text>
            </div>
            <Typography.Paragraph>{tr('privacy')}</Typography.Paragraph>
            <Typography.Paragraph>{tr('retention')}</Typography.Paragraph>
            <Typography.Paragraph>{tr('close_help')}</Typography.Paragraph>
            <Typography.Paragraph type="secondary">{tr('limitation')}</Typography.Paragraph>
          </>
        )}
      </Space>
    </Card>
  );
};
