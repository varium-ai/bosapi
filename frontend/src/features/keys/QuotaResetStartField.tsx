import { Form, Switch } from 'antd';
import { KeyExpiryInput } from './KeyExpiryInput';
import { useI18n } from '../../i18n';

export function QuotaResetStartField() {
  const { t } = useI18n();
  const form = Form.useFormInstance();
  const start = Form.useWatch('quota_reset_start_at', form);
  return (
    <>
      <Form.Item label={t('keys.custom_quota_cycle', 'Custom quota cycle')}>
        <Switch
          checked={!!start}
          onChange={enabled =>
            form.setFieldValue('quota_reset_start_at', enabled ? new Date().toISOString() : null)
          }
        />
      </Form.Item>
      <Form.Item
        name="quota_reset_start_at"
        label={t('keys.quota_reset_start_at', 'Cycle start time')}
        extra={t(
          'keys.quota_cycle_help',
          'Default: UTC calendar. Custom cycles repeat every 24 hours, 7 days and 30 days from this time. Before the start time, periodic limits do not apply.',
        )}
      >
        <KeyExpiryInput disabled={!start} allowClear={false} />
      </Form.Item>
    </>
  );
}
