import React from 'react';
import { Alert, Descriptions, Typography } from 'antd';
import { useI18n } from '../../i18n';
import { analyzeTrace, type RequestLogEntry } from './diagnostics';

const metadataFields = [
  'method',
  'endpoint',
  'model',
  'provider_name',
  'provider_id',
  'upstream_url',
  'status',
  'result_status',
  'outcome',
  'error_code',
  'failure_stage',
  'error_param',
  'error_reason',
  'upstream_status',
  'upstream_request_id',
  'stream_frame',
  'stream_event',
  'stream_block_index',
  'stream_block_type',
  'duration_ms',
  'timeout_ms',
  'timeout_scope',
  'deadline_remaining_ms',
  'stream',
  'phase',
];

export const TraceMetadata: React.FC<{ fields: Record<string, string> }> = ({ fields }) => {
  const { t, formatNumber } = useI18n();
  return (
    <Descriptions
      size="small"
      column={{ xs: 1, sm: 2, lg: 3 }}
      style={{ marginTop: 12 }}
      items={metadataFields
        .filter(
          key =>
            fields[key] !== undefined && !(key === 'phase' && fields[key] === 'response_headers'),
        )
        .map(key => {
          let value = fields[key];
          if (key === 'phase') value = t(`ux.phase_${value}`, value);
          if (key === 'timeout_scope') value = t(`ux.timeout_scope_${value}`, value);
          if (key === 'stream') value = t(value === 'true' ? 'common.yes' : 'common.no', value);
          if (key === 'outcome') value = t(`ux.outcome_${value}`, value);
          if (key === 'failure_stage') value = t(`ux.stage_${value}`, value);
          if (key === 'deadline_remaining_ms' && value === '-1') value = t('ux.deadline_none');
          if (key === 'timeout_ms' && value === '0') value = t('ux.timeout_none');
          if (key.endsWith('_ms') && Number.isFinite(Number(value)))
            value = formatNumber(Number(value));
          return {
            key,
            label: t(`ux.field_${key}`),
            children: <span style={{ overflowWrap: 'anywhere' }}>{value}</span>,
          };
        })}
    />
  );
};

export const TraceDiagnosis: React.FC<{ rows: RequestLogEntry[]; incomplete?: boolean }> = ({
  rows,
  incomplete = false,
}) => {
  const { t, formatDateTime } = useI18n();
  const analysis = analyzeTrace(rows, incomplete);
  const { fields } = analysis;
  return (
    <div style={{ marginBottom: 16 }}>
      <Alert
        showIcon
        type={
          analysis.diagnosis === 'success'
            ? 'success'
            : analysis.diagnosis === 'insufficient'
              ? 'info'
              : 'error'
        }
        message={`${t('ux.diagnosis')}: ${t(`ux.diagnosis_${analysis.diagnosis}`)}`}
        description={
          <>
            <p style={{ margin: '8px 0' }}>
              <strong>{t('ux.next_check')}: </strong>
              {t(`ux.hint_${analysis.diagnosis}`)}
            </p>
            {analysis.fault && (
              <div>
                <strong>{t('ux.evidence')}: </strong>
                {formatDateTime(analysis.fault.created_at)} ·{' '}
                <Typography.Text code>{analysis.fault.event}</Typography.Text>
                <div style={{ overflowWrap: 'anywhere', marginTop: 6 }}>
                  {analysis.fault.message}
                </div>
              </div>
            )}
            {!fields.upstream_status && (
              <div style={{ marginTop: 8 }}>{t('ux.no_upstream_status')}</div>
            )}
            {incomplete && <div style={{ marginTop: 8 }}>{t('ux.incomplete')}</div>}
          </>
        }
      />
      <TraceMetadata fields={fields} />
    </div>
  );
};
