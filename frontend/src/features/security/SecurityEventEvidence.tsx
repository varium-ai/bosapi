import React from 'react';
import { securityEventName } from '../common/ux';
import { Alert, Card, Descriptions, Space, Spin, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';
import { RecordDetails } from '../common/RecordDetails';

interface Evidence {
  source: string;
  path: string;
  start_line: number;
  start_column: number;
  end_line: number;
  end_column: number;
  reason_code: string;
  reason: string;
  category: string;
  trigger?: string;
  snippet: string;
  visibility: string;
}

interface EventDetail {
  rule_name?: string;
  rule_id: string;
  rule_version: number;
  processing_result: string;
  evidence_status: string;
  evidence_count: number;
  evidence_limit: number;
  evidence_expires_at?: string;
  hit_count: number;
  evidence: Evidence[];
}

// Only the administrator event drawer requests evidence. Key pages and list
// endpoints keep their existing metadata-only presentation.
export const SecurityEventEvidence: React.FC<{ eventId: number }> = ({ eventId }) => {
  const { t, formatDateTime } = useI18n();
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: ['security-event-detail', eventId],
    queryFn: async () =>
      (await apiClient.get(`/admin/security-events/${eventId}`)).data.data as EventDetail,
    gcTime: 0,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });

  if (isPending) return <Spin style={{ display: 'block', margin: 24 }} />;
  if (isError || !data) return <LoadError retry={refetch} />;

  const status =
    data.evidence_expires_at && Date.parse(data.evidence_expires_at) <= Date.now()
      ? 'expired'
      : data.evidence_status;
  const evidence = status === 'available' ? data.evidence : [];
  const currentInputOnly = data.rule_id === 'prompt_secret' && data.rule_version >= 2;

  return (
    <Space direction="vertical" size={16} style={{ width: '100%', marginBottom: 20 }}>
      <Typography.Title level={5} style={{ margin: 0 }}>
        {t('security.evidence_title')}
      </Typography.Title>
      <Alert type="info" showIcon message={t('security.evidence_privacy')} />
      <RecordDetails
        items={[
          {
            key: 'rule',
            label: t('security.col_rule'),
            children: `${securityEventName(data.rule_name, data.rule_id)}${data.rule_version ? ` (v${data.rule_version})` : ''}`,
          },
          ...(evidence.some(item => item.source === 'prompt')
            ? [
                {
                  key: 'scope',
                  label: t('security.evidence_scope'),
                  children: t(
                    currentInputOnly
                      ? 'security.evidence_request_scope'
                      : 'security.evidence_legacy_request_scope',
                  ),
                },
              ]
            : []),
          {
            key: 'processing',
            label: t('security.processing_result'),
            children: t(
              `security.processing_${data.processing_result || 'unknown'}`,
              t('security.processing_unknown'),
            ),
          },
          {
            key: 'count',
            label: t('security.evidence_count'),
            children: t('security.evidence_count_value')
              .replace('{count}', String(evidence.length))
              .replace('{total}', String(data.hit_count))
              .replace('{limit}', String(data.evidence_limit)),
          },
          ...(data.evidence_expires_at
            ? [
                {
                  key: 'expires',
                  label: t('security.evidence_expires'),
                  children: formatDateTime(data.evidence_expires_at),
                },
              ]
            : []),
        ]}
      />
      {status !== 'available' && (
        <Alert
          type="info"
          message={t(`security.evidence_${status}`, t('security.evidence_unavailable'))}
        />
      )}
      {evidence.map((item, index) => (
        <Card
          key={`${item.path}-${index}`}
          size="small"
          title={t('security.evidence_item').replace('{index}', String(index + 1))}
        >
          <Descriptions
            column={1}
            size="small"
            items={[
              {
                key: 'source',
                label: t('security.evidence_source'),
                children: t(
                  item.source === 'prompt' && currentInputOnly
                    ? 'security.source_current_prompt'
                    : `security.source_${item.source}`,
                  item.source,
                ),
              },
              {
                key: 'path',
                label: t('security.evidence_path'),
                children: (
                  <Typography.Text code style={{ overflowWrap: 'anywhere' }}>
                    {item.path}
                  </Typography.Text>
                ),
              },
              {
                key: 'location',
                label: t('security.evidence_location'),
                children:
                  item.start_line > 0
                    ? t('security.evidence_position')
                        .replace('{line}', String(item.start_line))
                        .replace('{column}', String(item.start_column))
                        .replace('{endLine}', String(item.end_line))
                        .replace('{endColumn}', String(item.end_column))
                    : t('security.evidence_field_only'),
              },
              {
                key: 'reason',
                label: t('security.col_reason'),
                children: (
                  <div style={{ width: '100%', minWidth: 0 }}>
                    <div>{t(`security.reason_${item.reason_code}`, item.reason)}</div>
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      {item.reason_code}
                    </Typography.Text>
                  </div>
                ),
              },
              {
                key: 'snippet',
                label: t('security.evidence_snippet'),
                children:
                  item.visibility === 'hidden' ? (
                    t('security.evidence_hidden')
                  ) : (
                    <Typography.Text
                      code
                      style={{ display: 'block', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
                    >
                      {item.snippet
                        .split('[Credential hidden]')
                        .join(t('security.credential_hidden'))
                        .split('[Match hidden]')
                        .join(t('security.match_hidden'))}
                    </Typography.Text>
                  ),
              },
            ]}
          />
          {item.category === 'credential' && item.visibility !== 'masked_context' && (
            <Typography.Paragraph type="secondary" style={{ margin: '8px 0 0' }}>
              {t('security.evidence_legacy_context')}
            </Typography.Paragraph>
          )}
        </Card>
      ))}
    </Space>
  );
};
