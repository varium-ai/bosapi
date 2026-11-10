import { compactKey } from '../common/display';
import React, { useEffect, useRef, useState } from 'react';
import {
  Card,
  Form,
  InputNumber,
  Button,
  Space,
  Typography,
  Row,
  Col,
  Tag,
  Alert,
  Select,
  Divider,
  message,
  Descriptions,
  Badge,
} from 'antd';
import { SaveOutlined, ApartmentOutlined, KeyOutlined, GlobalOutlined } from '@ant-design/icons';
import { RefreshButton } from '../common/RefreshButton';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import client from '../../api/client';
import { useI18n } from '../../i18n';
import { useSearchParams, useOutletContext } from 'react-router-dom';
import { LoadError, PageHelp } from '../common/ConsoleUX';
import { loadSetupPages } from '../onboarding/setupQueries';
import {
  quotaScope,
  resolveQuotaSelection,
  rateLimitQueryOptions,
  organizationQuotaValues,
  organizationQuotaUpdate,
  millionTokenInputValue,
  tokensFromMillionInput,
  validQuota,
  PERIODS,
  type QuotaUser,
  type OrganizationQuotaValues,
} from './organizationQuota';
import { OrganizationQuotaUsage } from './OrganizationQuotaUsage';

const { Text } = Typography;
const MAX_QPS = 1_000_000;
interface KeyOptionItem {
  id: number;
  name: string;
  key_prefix?: string;
  key_suffix?: string;
  masked_key?: string;
  status?: string;
}
interface OrganizationOption {
  id: number;
  name: string;
}

export const RateLimitsPage: React.FC = () => {
  const { user } = useOutletContext<{ user?: QuotaUser | null }>();
  if (!user || !['super_admin', 'org_admin'].includes(user.role)) return null;
  return <ScopedRateLimits key={JSON.stringify(quotaScope(user))} user={user} />;
};

const ScopedRateLimits: React.FC<{ user: QuotaUser }> = ({ user }) => {
  const [params, setParams] = useSearchParams();
  const superAdmin = user.role === 'super_admin';
  const organizationsQuery = useQuery({
    queryKey: ['rate-limit-organizations', ...quotaScope(user)],
    queryFn: async ({ signal }) => {
      if (superAdmin) return loadSetupPages<OrganizationOption>('/admin/organizations', signal);
      const response = await client.get(`/admin/organizations/${user.organization_id}`, { signal });
      const organization: OrganizationOption = response.data?.data;
      if (organization?.id !== user.organization_id) throw new Error('Invalid organization');
      return [organization];
    },
    enabled: superAdmin || user.organization_id > 0,
    retry: false,
  });
  const selection = resolveQuotaSelection(user, params, organizationsQuery.data?.[0]?.id);
  return (
    <RateLimitsEditor
      key={JSON.stringify([selection.organizationId, selection.keyId])}
      user={user}
      selectedOrgId={selection.organizationId}
      selectedKeyId={selection.keyId}
      organizations={(organizationsQuery.data ?? []).filter(
        organization => superAdmin || organization.id === user.organization_id,
      )}
      organizationsLoading={organizationsQuery.isFetching}
      organizationsError={organizationsQuery.isError}
      fetchLookups={() => {
        void organizationsQuery.refetch();
      }}
      select={(organizationId, keyId) => {
        const next = new URLSearchParams(params);
        if (organizationId) next.set('organization_id', String(organizationId));
        else next.delete('organization_id');
        if (keyId) next.set('key_id', String(keyId));
        else next.delete('key_id');
        setParams(next);
      }}
    />
  );
};

const RateLimitsEditor: React.FC<{
  user: QuotaUser;
  selectedOrgId?: number;
  selectedKeyId?: number;
  organizations: OrganizationOption[];
  organizationsLoading: boolean;
  organizationsError: boolean;
  fetchLookups: () => void;
  select: (organizationId?: number, keyId?: number) => void;
}> = ({
  user,
  selectedOrgId,
  selectedKeyId,
  organizations,
  organizationsLoading,
  organizationsError,
  fetchLookups,
  select,
}) => {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const superAdmin = user.role === 'super_admin';
  const qpsRangeMessage = t('ratelimits.qps_range');
  const qpsRangeRule = { type: 'integer' as const, min: 0, max: MAX_QPS, message: qpsRangeMessage };
  const formatKeyDisplay = compactKey;
  const [savingScope, setSavingScope] = useState<'global' | 'org' | 'key' | null>(null);
  const saving = savingScope !== null;
  const saveInFlight = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [globalForm] = Form.useForm();
  const [orgForm] = Form.useForm<OrganizationQuotaValues>();
  const [keyForm] = Form.useForm();

  // Keep the organization snapshot independent of the selected key.
  const organizationQuery = useQuery(rateLimitQueryOptions(user, selectedOrgId));
  const keysQuery = useQuery({
    queryKey: ['rate-limit-keys', ...quotaScope(user), selectedOrgId],
    queryFn: ({ signal }) =>
      loadSetupPages<KeyOptionItem>('/admin/keys', signal, { organization_id: selectedOrgId }),
    enabled: superAdmin && !!selectedOrgId,
    retry: false,
  });
  const keys = keysQuery.data ?? [];
  const validKey = !!selectedKeyId && keys.some(key => key.id === selectedKeyId);
  const keyQuery = useQuery({
    ...rateLimitQueryOptions(user, selectedOrgId, selectedKeyId),
    enabled: superAdmin && !!selectedOrgId && validKey,
  });
  const keysLoading = keysQuery.isFetching;
  const keysError = keysQuery.isError;
  useEffect(() => {
    if (selectedKeyId && keysQuery.isSuccess && !validKey) select(selectedOrgId);
  }, [selectedKeyId, keysQuery.isSuccess, validKey, selectedOrgId, select]);
  const isKeysPending = keysLoading || (organizationsLoading && !selectedOrgId);
  const loading = organizationQuery.isPending || organizationQuery.isFetching;
  const loadError = organizationQuery.isError;
  const snapshot = organizationQuery.data;
  const editable = superAdmin && !loading && !loadError && !!snapshot && !saving;
  const keyEditable =
    editable && validKey && !keysError && keyQuery.isSuccess && !keyQuery.isFetching;
  const [formReady, setFormReady] = useState(false);
  const [keyFormReady, setKeyFormReady] = useState(false);

  useEffect(() => {
    if (!superAdmin || !snapshot) return;
    globalForm.setFieldsValue({ qps: snapshot.global?.qps ?? 0 });
    if (snapshot.organization)
      orgForm.setFieldsValue(organizationQuotaValues(snapshot.organization));
    setFormReady(true);
  }, [superAdmin, snapshot, organizationQuery.dataUpdatedAt, globalForm, orgForm]);
  useEffect(() => {
    if (!keyQuery.data?.key) return;
    keyForm.setFieldsValue({
      ...keyQuery.data.key,
      quota_5h: keyQuery.data.key.quota_5h ?? null,
      quota_1d: keyQuery.data.key.quota_1d ?? null,
      quota_7d: keyQuery.data.key.quota_7d ?? null,
    });
    setKeyFormReady(true);
  }, [keyQuery.data, keyQuery.dataUpdatedAt, keyForm]);

  const fetchRateLimits = () => {
    void organizationQuery.refetch();
    if (validKey) void keyQuery.refetch();
  };
  const save = async (scope: 'global' | 'org' | 'key', payload: unknown, success: string) => {
    if (!superAdmin || saveInFlight.current || !mounted.current) return;
    saveInFlight.current = true;
    setSavingScope(scope);
    try {
      await client.put('/admin/rate-limits', payload);
      // Invalidate the organization snapshot shared with the home summary as well.
      await queryClient.invalidateQueries({
        queryKey:
          scope === 'global'
            ? ['organization-rate-limits', ...quotaScope(user)]
            : ['organization-rate-limits', ...quotaScope(user), selectedOrgId],
      });
      if (mounted.current) message.success(success);
    } catch (err: any) {
      if (mounted.current) message.error(err?.userMessage || t('ratelimits.save_failed'));
    } finally {
      saveInFlight.current = false;
      if (mounted.current) setSavingScope(null);
    }
  };
  const handleSaveGlobal = async (values: { qps: number }) => {
    if (!superAdmin || loading || saving || !snapshot || loadError) return;
    if (!formReady) return;
    await save('global', { global: { qps: values.qps } }, t('ratelimits.global_updated'));
  };
  const handleSaveOrg = async (values: OrganizationQuotaValues) => {
    if (
      !superAdmin ||
      !editable ||
      !formReady ||
      !selectedOrgId ||
      snapshot?.organization?.organization_id !== selectedOrgId ||
      Number(values.organization_id) !== selectedOrgId
    )
      return;
    try {
      await save(
        'org',
        organizationQuotaUpdate(values),
        t('ux.org_limits_updated').replace(
          '{name}',
          organizations.find(org => org.id === selectedOrgId)?.name || t('ux.name_unavailable'),
        ),
      );
    } catch {
      message.error(t('ratelimits.org_invalid_quota'));
    }
  };
  const handleSaveKey = async (values: {
    key_id: number;
    qps: number;
    quota_5h?: number | null;
    quota_1d?: number | null;
    quota_7d?: number | null;
  }) => {
    if (!superAdmin || !keyEditable || !keyFormReady || Number(values.key_id) !== selectedKeyId)
      return;
    await save(
      'key',
      {
        keys: [
          {
            key_id: values.key_id,
            qps: values.qps,
            quota_5h: values.quota_5h ?? null,
            quota_1d: values.quota_1d ?? null,
            quota_7d: values.quota_7d ?? null,
          },
        ],
      },
      t('ux.key_limits_updated').replace(
        '{name}',
        keys.find(key => key.id === selectedKeyId)?.name || t('ux.name_unavailable'),
      ),
    );
  };
  return (
    <Card
      aria-busy={loading}
      title={t('ratelimits.title', 'Rate Limits & Rolling Quotas')}
      extra={
        <RefreshButton
          onClick={() => {
            fetchLookups();
            if (superAdmin && selectedOrgId) void keysQuery.refetch();
            fetchRateLimits();
          }}
          loading={loading || organizationsLoading || keysLoading}
        />
      }
    >
      {loadError && (
        <LoadError
          retry={() => {
            fetchRateLimits();
          }}
        />
      )}

      <div className="rate-limits-status-slot">
        {snapshot && !loadError && (
          <Alert
            type={snapshot?.degraded ? 'warning' : 'success'}
            showIcon
            message={
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <span style={{ fontWeight: 600 }}>
                  {t('ratelimits.runtime_status', 'Limiter Runtime Status:')}
                </span>
                <Tag
                  style={{
                    background: snapshot?.degraded ? '#fffbeb' : '#dcfce7',
                    color: snapshot?.degraded ? '#92400e' : '#15803d',
                    border: `1px solid ${snapshot?.degraded ? '#fde68a' : '#bbf7d0'}`,
                    margin: 0,
                    fontSize: 12,
                    fontWeight: 600,
                    borderRadius: 4,
                    padding: '1px 8px',
                  }}
                >
                  {snapshot?.runtime_state || (snapshot?.degraded ? 'DEGRADED' : 'redis_counter')}
                </Tag>
                <span style={{ color: snapshot?.degraded ? '#92400e' : '#166534', fontSize: 13 }}>
                  {t(
                    'ratelimits.persisted_note',
                    'Persisted rate limits in MySQL are active and enforced by the gateway.',
                  )}
                </span>
              </div>
            }
          />
        )}
      </div>

      <Row gutter={[24, 24]}>
        {superAdmin && (
          <Col xs={24} md={12}>
            <Card
              type="inner"
              title={
                <Space>
                  <GlobalOutlined />
                  <span>{t('ratelimits.global_card_title', 'Global Concurrent Sessions')}</span>
                </Space>
              }
              style={{ height: '100%', display: 'flex', flexDirection: 'column' }}
              styles={{
                body: {
                  flex: 1,
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'space-between',
                },
              }}
            >
              <div>
                <Descriptions size="small" column={1} style={{ marginBottom: 16 }}>
                  <Descriptions.Item
                    label={t('ratelimits.configured_global_qps', 'Configured Global Concurrency')}
                  >
                    <Tag color="blue">{`${snapshot?.global?.qps ?? 0} sessions`}</Tag>
                  </Descriptions.Item>
                  <Descriptions.Item label={t('ratelimits.current_live_qps', 'Current Active Sessions')}>
                    {snapshot?.global?.current_qps ??
                      t('ratelimits.persisted_mode', 'N/A (Persisted Mode)')}
                  </Descriptions.Item>
                </Descriptions>

                <Divider style={{ margin: '12px 0' }} />

                <Form
                  name="global-limit"
                  form={globalForm}
                  layout="vertical"
                  initialValues={{ qps: snapshot?.global?.qps ?? 0 }}
                  onFinish={handleSaveGlobal}
                  disabled={!editable || !formReady}
                >
                  <Form.Item
                    name="qps"
                    label={t('ratelimits.global_qps_label', 'Global concurrent sessions (0 = unlimited)')}
                    rules={[
                      {
                        required: true,
                        message: t('ratelimits.enter_global_qps', 'Enter concurrency limit'),
                      },
                      qpsRangeRule,
                    ]}
                    extra={qpsRangeMessage}
                  >
                    <InputNumber
                      min={0}
                      max={MAX_QPS}
                      precision={0}
                      maxLength={7}
                      style={{ width: '100%' }}
                      addonAfter="sessions"
                    />
                  </Form.Item>
                  <div style={{ marginTop: 8 }}>
                    <Button
                      type="primary"
                      htmlType="submit"
                      icon={<SaveOutlined />}
                      loading={savingScope === 'global'}
                      disabled={!editable || !formReady}
                    >
                      {t('ratelimits.save_global_btn', 'Save Global Limit')}
                    </Button>
                  </div>
                </Form>
              </div>
            </Card>
          </Col>
        )}

        {/* Organization Rate Limit */}
        <Col xs={24} md={superAdmin ? 12 : 24}>
          <Card
            type="inner"
            title={
              <Space>
                <ApartmentOutlined />
                <span>{t('ratelimits.org_card_title', 'Organization Concurrency & Period Quotas')}</span>
              </Space>
            }
            style={{ height: '100%', display: 'flex', flexDirection: 'column' }}
            styles={{
              body: {
                flex: 1,
                display: 'flex',
                flexDirection: 'column',
                justifyContent: 'space-between',
              },
            }}
          >
            <div>
              <div style={{ marginBottom: 16 }}>
                <Text strong style={{ display: 'block', marginBottom: 6 }}>
                  {t('ratelimits.select_org_label', 'Select Target Organization')}
                </Text>
                <Select
                  style={{ width: '100%' }}
                  aria-label={t('ratelimits.select_org_label', 'Select Target Organization')}
                  placeholder={t(
                    'ratelimits.select_org_placeholder',
                    'Select organization to inspect/edit',
                  )}
                  value={selectedOrgId}
                  loading={organizationsLoading}
                  disabled={
                    !superAdmin ||
                    saving ||
                    organizationsError ||
                    (!organizationsLoading && organizations.length === 0)
                  }
                  onChange={val => select(val)}
                  options={organizations.map(o => ({
                    value: o.id,
                    rawOrg: o,
                    label: o.name,
                  }))}
                  optionRender={option => {
                    const o = option.data.rawOrg;
                    if (!o) return option.label;
                    return (
                      <Space size={8}>
                        <Text strong>{o.name}</Text>
                      </Space>
                    );
                  }}
                  labelRender={props => {
                    const o = organizations.find(item => item.id === props.value);
                    if (!o) return props.label;
                    return (
                      <Space size={8} align="center">
                        <Text strong>{o.name}</Text>
                      </Space>
                    );
                  }}
                  showSearch
                  filterOption={(input, option) => {
                    const o = option?.rawOrg;
                    if (!o) return false;
                    const query = input.trim().toLowerCase();
                    return String(o.id).includes(query) || o.name.toLowerCase().includes(query);
                  }}
                />
                {organizationsError && (
                  <div style={{ marginTop: 8 }}>
                    <LoadError
                      retry={() => {
                        fetchLookups();
                      }}
                    />
                  </div>
                )}
              </div>

              <Divider style={{ margin: '12px 0' }} />

              {!superAdmin && (
                <Alert
                  type="info"
                  showIcon
                  message={t('ratelimits.org_readonly_hint')}
                  style={{ marginBottom: 16 }}
                />
              )}
              {snapshot?.organization && !loadError && (
                <OrganizationQuotaUsage
                  organization={snapshot.organization}
                  millionTokens={!superAdmin}
                />
              )}
              <Divider style={{ margin: '12px 0' }} />
              {superAdmin ? (
                <Form
                  name="organization-limit"
                  form={orgForm}
                  layout="vertical"
                  onFinish={handleSaveOrg}
                  disabled={!editable || !formReady || !selectedOrgId}
                >
                  <Form.Item name="organization_id" hidden>
                    <InputNumber />
                  </Form.Item>
                  <Form.Item
                    name="qps"
                    label={t('ratelimits.org_qps_label')}
                    rules={[
                      { required: true, message: t('ratelimits.enter_org_qps') },
                      qpsRangeRule,
                    ]}
                    extra={qpsRangeMessage}
                  >
                    <InputNumber
                      min={0}
                      max={MAX_QPS}
                      precision={0}
                      style={{ width: '100%' }}
                      addonAfter="sessions"
                    />
                  </Form.Item>
                  <Text type="secondary">{t('ratelimits.org_quota_hint')}</Text>
                  <Row gutter={[16, 16]}>
                    {PERIODS.flatMap(period =>
                      (['tokens', 'amount'] as const).map(metric => (
                        <Col xs={24} sm={12} key={`${period}_${metric}`}>
                          <Form.Item
                            name={`${period}_quota_${metric}`}
                            label={t(`ratelimits.org_${period}_${metric}_label`)}
                            {...(metric === 'tokens'
                              ? {
                                  getValueProps: (value: number | null | undefined) => ({
                                    value: millionTokenInputValue(value),
                                  }),
                                  getValueFromEvent: (value: number | null) =>
                                    tokensFromMillionInput(value),
                                }
                              : {})}
                            rules={[
                              {
                                validator: (_, value) =>
                                  validQuota(value, metric === 'tokens')
                                    ? Promise.resolve()
                                    : Promise.reject(new Error(t('ratelimits.org_invalid_quota'))),
                              },
                            ]}
                          >
                            <InputNumber
                              min={0}
                              max={
                                metric === 'tokens'
                                  ? Number.MAX_SAFE_INTEGER / 1_000_000
                                  : undefined
                              }
                              precision={metric === 'tokens' ? 6 : undefined}
                              style={{ width: '100%' }}
                              placeholder={t('keys.unlimited')}
                              addonAfter={metric === 'tokens' ? 'M Token' : 'USD'}
                            />
                          </Form.Item>
                        </Col>
                      )),
                    )}
                  </Row>
                  <Button
                    type="primary"
                    htmlType="submit"
                    icon={<SaveOutlined />}
                    loading={savingScope === 'org'}
                    disabled={!editable || !formReady || !selectedOrgId}
                  >
                    {t('ratelimits.save_org_btn')}
                  </Button>
                </Form>
              ) : snapshot?.organization && !loadError ? (
                <Descriptions size="small" column={1}>
                  <Descriptions.Item label={t('ratelimits.org_qps_label')}>
                    {snapshot.organization.qps === 0
                      ? t('keys.unlimited')
                      : `${snapshot.organization.qps} sessions`}
                  </Descriptions.Item>
                </Descriptions>
              ) : null}
            </div>
          </Card>
        </Col>
      </Row>

      {superAdmin && (
        <Row gutter={[24, 24]} style={{ marginTop: 24 }}>
          <Col span={24}>
            <Card
              type="inner"
              title={
                <Space>
                  <KeyOutlined />
                  <span>{t('ratelimits.key_card_title', 'API Key Concurrency & Token Quotas')}</span>
                </Space>
              }
            >
              <div style={{ maxWidth: 600, marginBottom: 16 }}>
                <Text strong style={{ display: 'block', marginBottom: 6 }}>
                  {t('ratelimits.select_key_label', 'Select Target API Key')}
                </Text>
                <Select
                  style={{ width: '100%' }}
                  aria-label={t('ratelimits.select_key_label', 'Select Target API Key')}
                  placeholder={
                    keysError
                      ? t('ux.load_error')
                      : !isKeysPending && !selectedOrgId
                        ? t(
                            'ratelimits.select_org_placeholder',
                            'Select organization to inspect/edit',
                          )
                        : !isKeysPending && keys.length === 0
                          ? t('ratelimits.no_keys_in_org', 'No API keys in current organization')
                          : t('ratelimits.select_key_placeholder', 'Select API key to inspect/edit')
                  }
                  value={selectedKeyId}
                  loading={isKeysPending}
                  disabled={
                    saving || keysLoading || keysError || !selectedOrgId || keys.length === 0
                  }
                  onChange={val => select(selectedOrgId, val)}
                  options={keys.map(k => ({
                    value: k.id,
                    rawKey: k,
                    label: `${k.name} (${formatKeyDisplay(k)})${k.key_suffix ? '' : ` · ${t('ratelimits.key_suffix_unavailable', 'Last 4 not recorded')}`}`,
                  }))}
                  optionRender={option => {
                    const k = option.data.rawKey;
                    if (!k) return option.label;
                    return (
                      <div
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          padding: '3px 0',
                        }}
                      >
                        <Text
                          strong
                          ellipsis
                          style={{ maxWidth: 240, minWidth: 0, marginRight: 12 }}
                        >
                          {k.name}
                        </Text>
                        <Space size={8} style={{ flexShrink: 0 }}>
                          <Text
                            type="secondary"
                            code
                            style={{
                              fontSize: 11,
                              background: 'rgba(0,0,0,0.03)',
                              padding: '1px 5px',
                              borderRadius: 4,
                            }}
                          >
                            {formatKeyDisplay(k)}
                          </Text>
                          {!k.key_suffix && (
                            <Text type="secondary" style={{ fontSize: 11 }}>
                              {t('ratelimits.key_suffix_unavailable', 'Last 4 not recorded')}
                            </Text>
                          )}
                          <Badge
                            status={k.status === 'disabled' ? 'default' : 'success'}
                            text={
                              <span
                                style={{
                                  fontSize: 11,
                                  color:
                                    k.status === 'disabled' ? 'var(--silver)' : 'var(--success)',
                                }}
                              >
                                {k.status === 'disabled'
                                  ? t('common.disabled', 'Disabled')
                                  : t('common.active', 'Active')}
                              </span>
                            }
                          />
                        </Space>
                      </div>
                    );
                  }}
                  labelRender={props => {
                    const k = keys.find(item => item.id === props.value);
                    if (!k) return props.label;
                    return (
                      <Space size={8} align="center">
                        <Text strong>{k.name}</Text>
                        <Text type="secondary" code style={{ fontSize: 12, marginLeft: 2 }}>
                          {formatKeyDisplay(k)}
                        </Text>
                        {!k.key_suffix && (
                          <Text type="secondary" style={{ fontSize: 11 }}>
                            {t('ratelimits.key_suffix_unavailable', 'Last 4 not recorded')}
                          </Text>
                        )}
                      </Space>
                    );
                  }}
                  popupMatchSelectWidth={false}
                  dropdownStyle={{
                    minWidth: 'min(480px, calc(100vw - 32px))',
                    maxWidth: 'calc(100vw - 32px)',
                  }}
                  showSearch
                  filterOption={(input, option) => {
                    const k = option?.rawKey;
                    if (!k) return false;
                    const query = input.trim().toLowerCase();
                    return (
                      String(k.id).includes(query) ||
                      `#${k.id}`.includes(query) ||
                      k.name.toLowerCase().includes(query) ||
                      Boolean(k.key_prefix && k.key_prefix.toLowerCase().includes(query)) ||
                      Boolean(k.key_suffix && k.key_suffix.toLowerCase().includes(query)) ||
                      Boolean(k.masked_key && k.masked_key.toLowerCase().includes(query))
                    );
                  }}
                />
                {keysError && (
                  <div style={{ marginTop: 8 }}>
                    <LoadError
                      retry={() => {
                        if (superAdmin && selectedOrgId) void keysQuery.refetch();
                      }}
                    />
                  </div>
                )}
                <Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
                  {t(
                    'ratelimits.key_suffix_hint',
                    'Legacy keys can record their last 4 characters when authenticated through an API request or key login. Click Refresh afterwards.',
                  )}
                </Text>
              </div>

              <Divider style={{ margin: '16px 0 20px' }} />

              {keyQuery.isError && (
                <LoadError
                  retry={() => {
                    void keyQuery.refetch();
                  }}
                />
              )}
              <PageHelp note="ux.independent_save" />
              <Form
                name="key-limit"
                form={keyForm}
                layout="vertical"
                onFinish={handleSaveKey}
                disabled={!keyEditable || !keyFormReady}
              >
                <Form.Item name="key_id" hidden>
                  <InputNumber />
                </Form.Item>

                <Row gutter={[20, 16]}>
                  <Col xs={24} sm={12} lg={6}>
                    <Form.Item
                      name="qps"
                      label={t('ratelimits.key_qps_label', 'Key concurrent sessions')}
                      rules={[
                        { required: true, message: t('ratelimits.enter_global_qps', 'Enter concurrency limit') },
                        qpsRangeRule,
                      ]}
                      extra={qpsRangeMessage}
                    >
                      <InputNumber
                        min={0}
                        max={MAX_QPS}
                        precision={0}
                        maxLength={7}
                        style={{ width: '100%' }}
                        addonAfter="sessions"
                      />
                    </Form.Item>
                  </Col>
                  <Col xs={24} sm={12} lg={6}>
                    <Form.Item
                      name="quota_5h"
                      label={t(
                        'ratelimits.quota_5h_label',
                        '5-Hour Rolling Token Quota (optional)',
                      )}
                    >
                      <InputNumber
                        min={0}
                        style={{ width: '100%' }}
                        placeholder={t('keys.unlimited', 'No limit')}
                        addonAfter="tokens"
                      />
                    </Form.Item>
                  </Col>
                  <Col xs={24} sm={12} lg={6}>
                    <Form.Item
                      name="quota_1d"
                      label={t(
                        'ratelimits.quota_1d_label',
                        '24-Hour Rolling Token Quota (optional)',
                      )}
                    >
                      <InputNumber
                        min={0}
                        style={{ width: '100%' }}
                        placeholder={t('keys.unlimited', 'No limit')}
                        addonAfter="tokens"
                      />
                    </Form.Item>
                  </Col>
                  <Col xs={24} sm={12} lg={6}>
                    <Form.Item
                      name="quota_7d"
                      label={t('ratelimits.quota_7d_label', '7-Day Rolling Token Quota (optional)')}
                    >
                      <InputNumber
                        min={0}
                        style={{ width: '100%' }}
                        placeholder={t('keys.unlimited', 'No limit')}
                        addonAfter="tokens"
                      />
                    </Form.Item>
                  </Col>
                </Row>

                <div style={{ marginTop: 8 }}>
                  <Button
                    type="primary"
                    htmlType="submit"
                    icon={<SaveOutlined />}
                    loading={savingScope === 'key'}
                    disabled={!keyEditable || !keyFormReady}
                  >
                    {t('ratelimits.save_key_btn', 'Save Key Limits & Quotas')}
                  </Button>
                </div>
              </Form>
            </Card>
          </Col>
        </Row>
      )}
    </Card>
  );
};
export default RateLimitsPage;
