import React from 'react';
import { Alert, Col, Divider, Form, InputNumber, Row, Select, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { organizationOptionsQueryOptions } from '../../api/adminQueries';
import { useI18n } from '../../i18n';
import { AdvancedFields } from '../common/ConsoleUX';
import { KeyExpiryInput } from './KeyExpiryInput';
import { QuotaResetStartField } from './QuotaResetStartField';
import { KeyAccessFields, KeyAccessReadiness } from './KeyAccessFields';

const { Text } = Typography;

export const KeyRollingLimitFields: React.FC<{
  showTokenLimits?: boolean;
}> = ({ showTokenLimits = true }) => {
  const { t } = useI18n();
  return (
    <>
      <Divider orientation="left">
        {t('ratelimits.key_concurrency_title', 'Concurrent Requests')}
      </Divider>
      <Form.Item
        name="rate_limit_qps"
        label={t('ratelimits.key_qps_label', 'Key concurrent sessions')}
      >
        <InputNumber
          min={0}
          max={1_000_000}
          precision={0}
          style={{ width: '100%' }}
          placeholder={t('keys.unlimited', 'No limit')}
          addonAfter={t('ratelimits.sessions_unit', 'sessions')}
        />
      </Form.Item>
      {showTokenLimits && (
        <>
          <Divider orientation="left">{t('keys.rolling_token_limits', 'Rolling Quotas')}</Divider>
          <Alert
            type="info"
            showIcon
            message={t('keys.rolling_quota_help')}
            style={{ marginBottom: 16 }}
          />
          {(['5h', '1d', '7d'] as const).map(period => (
            <Row gutter={16} align="top" key={period}>
              <Col span={12}>
                <Form.Item
                  name={`quota_${period}`}
                  label={t(`keys.quota_${period}`, `${period} Token limit`)}
                >
                  <InputNumber
                    min={0}
                    precision={0}
                    style={{ width: '100%' }}
                    placeholder={t('keys.quota_tokens_placeholder', 'Unlimited')}
                    addonAfter="tokens"
                  />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item
                  name={`quota_${period}_amount`}
                  label={t(`keys.quota_${period}_amount`, `${period} Amount limit (USD)`)}
                >
                  <InputNumber
                    min={0}
                    precision={2}
                    style={{ width: '100%' }}
                    placeholder={t('keys.quota_amount_placeholder', 'Unlimited')}
                    addonAfter="USD"
                  />
                </Form.Item>
              </Col>
            </Row>
          ))}
        </>
      )}
    </>
  );
};

export const KeyConfigurationFields: React.FC<{
  enabled: boolean;
  currentUser: { id: number; organization_id?: number; role: string };
  advancedOpen?: boolean;
  showStatus?: boolean;
  showRollingLimits?: boolean;
  showTokenLimits?: boolean;
  showIPBlocklist?: boolean;
}> = ({
  enabled,
  currentUser,
  advancedOpen = false,
  showStatus = false,
  showRollingLimits = true,
  showTokenLimits = true,
  showIPBlocklist = true,
}) => {
  const { t } = useI18n();
  const form = Form.useFormInstance();
  const organizationID = Form.useWatch('organization_id', form);
  const canChangeOrganization = currentUser.role === 'super_admin';
  const { data: organizations = [] } = useQuery(organizationOptionsQueryOptions());
  const orgOptions = organizations.map(org => ({ label: org.name, value: org.id }));
  return (
    <>
      <Row gutter={16} align="top">
        <Col span={24}>
          <Form.Item
            name="organization_id"
            label={t('keys.field_org', 'Organization')}
            rules={[{ required: true }]}
            extra={t('keys.organization_immutable')}
          >
            <Select
              disabled={!canChangeOrganization}
              onChange={() => form.setFieldsValue({ allowed_provider_ids: [], allowed_models: [] })}
              style={{ width: '100%' }}
              options={orgOptions}
            />
          </Form.Item>
        </Col>
      </Row>
      <KeyAccessReadiness
        enabled={enabled}
        organizationID={organizationID}
        canManageOrganization={canChangeOrganization}
      />
      <QuotaResetStartField />
      <AdvancedFields defaultOpen={advancedOpen}>
        <KeyAccessFields enabled={enabled} organizationID={organizationID} />
        <Form.Item
          name="expires_at"
          label={t('common.expires_at')}
          extra={t('keys.expiration_help')}
        >
          <KeyExpiryInput />
        </Form.Item>
        <Alert type="info" message={t('ux.quota_note')} style={{ marginBottom: 16 }} />
        <Row gutter={16} align="top">
          <Col span={12}>
            <Form.Item
              name="quota_tokens"
              label={t('keys.field_quota_tokens', 'Token Quota (Optional)')}
            >
              <InputNumber
                min={1}
                style={{ width: '100%' }}
                placeholder={t('keys.quota_tokens_placeholder', 'Unlimited (e.g. 10,000,000)')}
              />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              name="quota_amount"
              label={t('keys.field_quota_amount', 'Amount Quota USD (Optional)')}
            >
              <InputNumber
                min={0}
                step={1}
                style={{ width: '100%' }}
                placeholder={t('keys.quota_amount_placeholder', 'Unlimited (e.g. 100.00)')}
              />
            </Form.Item>
          </Col>
        </Row>

        <Divider orientation="left">
          {t('keys.periodic_quotas', 'Periodic Quotas (Optional)')}
        </Divider>
        {(['daily', 'weekly', 'monthly'] as const).map(period => (
          <Row gutter={16} align="top" key={period}>
            <Col span={24}>
              <Text strong>{t(`keys.period_${period}`, period)}</Text>
            </Col>
            <Col span={12}>
              <Form.Item
                name={`${period}_quota_tokens`}
                label={t('keys.field_period_tokens', 'Token Limit')}
              >
                <InputNumber
                  min={0}
                  style={{ width: '100%' }}
                  placeholder={t('keys.quota_tokens_placeholder', 'Unlimited')}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name={`${period}_quota_amount`}
                label={t('keys.field_period_amount', 'Amount Limit (USD)')}
              >
                <InputNumber
                  min={0}
                  step={0.01}
                  style={{ width: '100%' }}
                  placeholder={t('keys.quota_amount_placeholder', 'Unlimited')}
                />
              </Form.Item>
            </Col>
          </Row>
        ))}

        <Form.Item
          name="ip_allowlist"
          label={t('keys.field_ip_allowlist', 'IP Allowlist (Optional CIDR / IPv4 / IPv6)')}
        >
          <Select
            mode="tags"
            placeholder={t(
              'keys.ip_placeholder',
              'Enter allowed IP addresses or CIDR blocks (empty allows any IP)',
            )}
          />
        </Form.Item>
        {showStatus && (
          <Form.Item name="status" label={t('common.status')}>
            <Select
              options={[
                { value: 'active', label: t('common.active') },
                { value: 'disabled', label: t('common.disabled') },
              ]}
            />
          </Form.Item>
        )}
        {showRollingLimits && <KeyRollingLimitFields showTokenLimits={showTokenLimits} />}
        {showIPBlocklist && (
          <Form.Item name="ip_blocklist" label={t('keys.field_ip_blocklist')}>
            <Select mode="tags" />
          </Form.Item>
        )}
      </AdvancedFields>
    </>
  );
};
