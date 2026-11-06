import { Space, Typography } from 'antd';
import { useI18n } from '../../i18n';
import { SegmentedControl } from '../common/SegmentedControl';

export const ipWindowPresets = [30, 60, 360, 720, 1440] as const;

export function KeyIPTimeRange({
  value,
  onChange,
}: {
  value: number;
  onChange: (value: number) => void;
}) {
  const { t } = useI18n();
  return (
    <Space direction="vertical" style={{ marginBottom: 16, maxWidth: '100%' }}>
      <SegmentedControl
        aria-label={t('keys.ip_window_label')}
        value={value}
        onChange={onChange}
        options={ipWindowPresets.map(minutes => ({
          value: minutes,
          label: t(`keys.ip_window_${minutes}`),
        }))}
      />
      <Typography.Text type="secondary">{t('keys.ip_window_cache_note')}</Typography.Text>
    </Space>
  );
}
