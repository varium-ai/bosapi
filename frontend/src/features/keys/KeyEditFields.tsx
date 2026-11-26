import React from 'react';
import {
  Alert,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Row,
  Select,
  Switch,
  Typography,
} from 'antd';
import { useI18n } from '../../i18n';
import { AdvancedFields } from '../common/ConsoleUX';
import { ResourceName } from '../common/ResourceName';
import { KeyExpiryInput } from './KeyExpiryInput';
import { KeyAccessFields } from './KeyAccessFields';
import { KeyRollingLimitFields } from './KeyConfigurationFields';
import { QuotaResetStartField } from './QuotaResetStartField';
import type { APIKeyRecord } from './types';

const { Text } = Typography;

export const KeyEditFields: React.FC<{
  record: APIKeyRecord;
  currentUser: { id: number; organization_id?: number; role: string };
  organizations: Array<{ id: number; name: string; status?: string }>;
  enabled: boolean;
  organizationID: number;
}> = ({ record, currentUser, organizations, enabled, organizationID }) => {
  const { t } = useI18n();
  return (
    <>
      <Form.Item
        name="name"
        label={t('keys.field_name', 'Key Name')}
        rules={[{ required: true, whitespace: true, message: t('keys.enter_key_name') }]}
      >
        <Input maxLength={100} />
      </Form.Item>
      {currentUser.role === 'super_admin' ? (
        <Form.Item name="organization_id" label={t('keys.field_org')} rules={[{ required: true }]}>
          <Select
            showSearch
            optionFilterProp="label"
            options={organizations
              .filter(organization => organization.status === 'active')
              .map(organization => ({ value: organization.id, label: organization.name }))}
          />
        </Form.Item>
      ) : (
        <Form.Item label={t('keys.field_org')}>
          <ResourceName kind="organizations" id={record.organization_id} />
        </Form.Item>
      )}
      <Form.Item
        name="security_inspection_upstream_enabled"
        label={t('keys.security_inspection_upstream', 'Upstream security inspection')}
        valuePropName="checked"
        extra={t('keys.security_inspection_upstream_help')}
      >
        <Switch />
      </Form.Item>
      <Form.Item
        name="security_inspection_downstream_enabled"
        label={t('keys.security_inspection_downstream', 'Downstream security inspection')}
        valuePropName="checked"
        extra={t('keys.security_inspection_downstream_help')}
      >
        <Switch />
      </Form.Item>
      <AdvancedFields>
        <KeyAccessFields enabled={enabled} organizationID={organizationID} />
        <Form.Item
          name="expires_at"
          label={t('common.expires_at')}
          extra={t('keys.expiration_help')}
        >
          <KeyExpiryInput />
        </Form.Item>
        <Alert type="info" message={t('ux.quota_note')} style={{ marginBottom: 16 }} />
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item name="quota_tokens" label={t('keys.field_quota_tokens')}>
              <InputNumber
                min={0}
                style={{ width: '100%' }}
                placeholder={t('keys.quota_tokens_placeholder')}
              />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="quota_amount" label={t('keys.field_quota_amount')}>
              <InputNumber
                min={0}
                style={{ width: '100%' }}
                placeholder={t('keys.quota_amount_placeholder')}
              />
            </Form.Item>
          </Col>
        </Row>
        <Divider orientation="left">
          {t('keys.periodic_quotas', 'Periodic Quotas (Optional)')}
        </Divider>
        <QuotaResetStartField />
        {(['daily', 'weekly', 'monthly'] as const).map(period => (
          <Row gutter={16} key={period}>
            <Col span={24}>
              <Text strong>{t(`keys.period_${period}`, period)}</Text>
            </Col>
            <Col span={12}>
              <Form.Item name={`${period}_quota_tokens`} label={t('keys.field_period_tokens')}>
                <InputNumber
                  min={0}
                  style={{ width: '100%' }}
                  placeholder={t('keys.quota_tokens_placeholder')}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name={`${period}_quota_amount`} label={t('keys.field_period_amount')}>
                <InputNumber
                  min={0}
                  style={{ width: '100%' }}
                  placeholder={t('keys.quota_amount_placeholder')}
                />
              </Form.Item>
            </Col>
          </Row>
        ))}
        <KeyRollingLimitFields />
        <Form.Item name="ip_allowlist" label={t('keys.field_ip_allowlist')}>
          <Select mode="tags" placeholder={t('keys.ip_placeholder')} />
        </Form.Item>
      </AdvancedFields>
    </>
  );
};
