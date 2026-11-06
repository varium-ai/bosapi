import { Alert } from 'antd';
import { useI18n } from '../../i18n';
import './usageSnapshotStatus.css';

export interface UsageSnapshot {
  snapshot_id?: string;
  generated_at?: string;
  valid_until?: string;
  status: string;
}
export function snapshotUsable(snapshot?: UsageSnapshot) {
  return (
    !snapshot ||
    (['ready', 'refreshing', 'refresh_failed'].includes(snapshot.status) &&
      !!snapshot.valid_until &&
      Date.parse(snapshot.valid_until) > Date.now())
  );
}
export function UsageSnapshotStatus({
  snapshot,
  note,
}: {
  snapshot?: UsageSnapshot;
  note?: string;
}) {
  const { t, formatDateTime } = useI18n();
  if (!snapshot && !note) return null;
  const valid = snapshotUsable(snapshot);
  const status = valid
    ? snapshot?.status
    : snapshot?.status === 'generating'
      ? 'generating'
      : 'expired';
  const generatedAt = snapshot?.generated_at;
  return (
    <Alert
      className="usage-snapshot-status"
      showIcon
      type={valid && status !== 'refresh_failed' ? 'info' : 'warning'}
      message={
        <div className="usage-snapshot-status__content">
          {note && <div className="usage-snapshot-status__note">{note}</div>}
          {snapshot && (
            <div className="usage-snapshot-status__metadata">
              <span>{t(`usage.snapshot_${status}`)}</span>
              {generatedAt && !generatedAt.startsWith('0001') && (
                <span className="usage-snapshot-status__timestamp">
                  {t('usage.snapshot_as_of')}:{' '}
                  <time dateTime={generatedAt}>{formatDateTime(generatedAt)}</time>
                </span>
              )}
            </div>
          )}
        </div>
      }
    />
  );
}
