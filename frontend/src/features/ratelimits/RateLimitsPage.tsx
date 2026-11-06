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
import { useQueryClient } from '@tanstack/react-query';
import { adminKeysQueryOptions, organizationOptionsQueryOptions } from '../../api/adminQueries';
import client from '../../api/client';
import { useI18n } from '../../i18n';
import { useSearchParams, useOutletContext } from 'react-router-dom';
import { LoadError, PageHelp } from '../common/ConsoleUX';

const { Text } = Typography;
const MAX_QPS = 1_000_000;

interface RateLimitSnapshot {
  degraded: boolean;
  runtime_state: string;
  global?: {
    qps: number;
    current_qps: number | null;
    remaining: number | null;
  };
  organization?: {
    organization_id: number;
    qps: number;
  } | null;
  key?: {
    key_id: number;
    qps: number;
    quota_5h?: number | null;
    quota_1d?: number | null;
    quota_7d?: number | null;
  } | null;
}

interface KeyOptionItem {
  id: number;
  name: string;
  key_prefix?: string;
  key_suffix?: string;
  masked_key?: string;
  status?: string;
}

let cachedOrganizations: { id: number; name: string }[] | null = null;
const keysCache = new Map<number, KeyOptionItem[]>();
let lastRateLimitSnapshot: RateLimitSnapshot | null = null;
const snapshotCache = new Map<string, RateLimitSnapshot>();
const getSnapshotCacheKey = (orgId?: number, keyId?: number) => `${orgId || 0}:${keyId || 0}`;

export const RateLimitsPage: React.FC = () => {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [params] = useSearchParams();
  const { user } = useOutletContext<{ user?: { role: string; organization_id: number } }>();
  const storedUser = React.useMemo(() => {
    try {
      return JSON.parse(localStorage.getItem('user_info') || 'null') as {
        role?: string;
        organization_id?: number;
      } | null;
    } catch {
      return null;
    }
  }, []);
  const effectiveUser = user ?? storedUser ?? undefined;
  const superAdmin = effectiveUser?.role === 'super_admin';
  const qpsRangeMessage = t(
    'ratelimits.qps_range',
    'QPS must be an integer between 0 and 1,000,000 (0 = unlimited)',
  );
  const qpsRangeRule = { type: 'integer' as const, min: 0, max: MAX_QPS, message: qpsRangeMessage };
  const initialOrganizations =
    queryClient.getQueryData<{ id: number; name: string }[]>(
      organizationOptionsQueryOptions().queryKey,
    ) ??
    cachedOrganizations ??
    [];
  const initialOrgId =
    Number(params.get('organization_id')) ||
    effectiveUser?.organization_id ||
    initialOrganizations[0]?.id ||
    undefined;
  const initialKeyId = Number(params.get('key_id')) || undefined;
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savingScope, setSavingScope] = useState<'global' | 'org' | 'key' | null>(null);
  const [snapshot, setSnapshot] = useState<RateLimitSnapshot | null>(
    () =>
      snapshotCache.get(getSnapshotCacheKey(initialOrgId, initialKeyId)) ?? lastRateLimitSnapshot,
  );

  const formatKeyDisplay = compactKey;
  const initialKeys = initialOrgId
    ? (queryClient.getQueryData<{ items: KeyOptionItem[] }>(
        adminKeysQueryOptions({ page: 1, pageSize: 100, organizationId: initialOrgId }).queryKey,
      )?.items ??
      keysCache.get(initialOrgId) ??
      [])
    : [];

  const [organizations, setOrganizations] = useState<{ id: number; name: string }[]>(
    () => initialOrganizations,
  );
  const [organizationsLoading, setOrganizationsLoading] = useState(
    () => initialOrganizations.length === 0,
  );
  const [organizationsError, setOrganizationsError] = useState(false);
  const [keys, setKeys] = useState<KeyOptionItem[]>(() => initialKeys);
  const [keysLoading, setKeysLoading] = useState(
    () => Boolean(initialOrgId || initialOrganizations.length === 0) && initialKeys.length === 0,
  );
  const [keysError, setKeysError] = useState(false);
  const [keysRefreshVersion, setKeysRefreshVersion] = useState(0);

  const [selectedOrgId, setSelectedOrgId] = useState<number | undefined>(() => initialOrgId);
  const [selectedKeyId, setSelectedKeyId] = useState<number | undefined>(() => initialKeyId);
  const rateLimitRequest = useRef(0);

  const [globalForm] = Form.useForm();
  const [orgForm] = Form.useForm();
  const [keyForm] = Form.useForm();

  const fetchLookups = async () => {
    setOrganizationsLoading(true);
    setOrganizationsError(false);
    try {
      const orgRes = await client.get('/admin/organizations', {
        params: { page: 1, page_size: 100 },
      });
      if (orgRes?.data?.data?.items) {
        const items = orgRes.data.data.items;
        cachedOrganizations = items;
        queryClient.setQueryData(organizationOptionsQueryOptions().queryKey, items);
        setOrganizations(items);
        if (items.length > 0) {
          setSelectedOrgId(current => current ?? items[0].id);
        }
      }
    } catch {
      setOrganizationsError(true);
    } finally {
      setOrganizationsLoading(false);
    }
  };

  const fetchRateLimits = async (orgId = selectedOrgId, keyId = selectedKeyId) => {
    const request = ++rateLimitRequest.current;
    setLoading(true);
    setLoadError(false);
    try {
      const params: Record<string, any> = {};
      if (orgId) params.organization_id = orgId;
      if (keyId) params.key_id = keyId;

      const res = await client.get('/admin/rate-limits', { params });
      if (request !== rateLimitRequest.current) return;
      const data: RateLimitSnapshot = res.data?.data;
      if (data) {
        lastRateLimitSnapshot = data;
        snapshotCache.set(getSnapshotCacheKey(orgId, keyId), data);
      }
      setSnapshot(data);

      globalForm.setFieldsValue({
        qps: data?.global?.qps ?? 0,
      });

      if (orgId) {
        orgForm.setFieldsValue({
          organization_id: orgId,
          qps: data?.organization?.qps ?? 0,
        });
      }

      if (keyId) {
        keyForm.setFieldsValue({
          key_id: keyId,
          qps: data?.key?.qps ?? 0,
          quota_5h: data?.key?.quota_5h ?? null,
          quota_1d: data?.key?.quota_1d ?? null,
          quota_7d: data?.key?.quota_7d ?? null,
        });
      }
    } catch (err: any) {
      if (request === rateLimitRequest.current) {
        setLoadError(true);
        message.error(err?.userMessage || 'Failed to load rate limit configuration');
      }
    } finally {
      if (request === rateLimitRequest.current) setLoading(false);
    }
  };

  useEffect(() => {
    fetchLookups();
  }, []);

  useEffect(() => {
    if (!selectedOrgId) {
      setKeys([]);
      setKeysLoading(false);
      setKeysError(false);
      return;
    }
    let cancelled = false;
    const fetchKeys = async () => {
      setKeysLoading(true);
      setKeysError(false);
      try {
        const res = await client.get('/admin/keys', {
          params: { page: 1, page_size: 100, organization_id: selectedOrgId },
        });
        if (cancelled) return;
        const items = res.data?.data?.items ?? [];
        keysCache.set(selectedOrgId, items);
        queryClient.setQueryData(
          adminKeysQueryOptions({ page: 1, pageSize: 100, organizationId: selectedOrgId }).queryKey,
          res.data?.data ?? { items, total: items.length },
        );
        setKeys(items);
        setSelectedKeyId(current =>
          current && items.some((item: KeyOptionItem) => item.id === current) ? current : undefined,
        );
      } catch (err: any) {
        if (!cancelled) {
          setKeysError(true);
          message.error(err?.userMessage || t('common.error', 'Failed to load API keys'));
        }
      } finally {
        if (!cancelled) setKeysLoading(false);
      }
    };
    fetchKeys();
    return () => {
      cancelled = true;
    };
  }, [selectedOrgId, keysRefreshVersion]);

  const orgResolved = Boolean(selectedOrgId) || !organizationsLoading;
  const isKeysPending = keysLoading || (organizationsLoading && !selectedOrgId);

  useEffect(() => {
    if (!orgResolved) return;
    fetchRateLimits(selectedOrgId, selectedKeyId);
    return () => {
      rateLimitRequest.current++;
    };
  }, [selectedOrgId, selectedKeyId, orgResolved]);

  const handleSaveGlobal = async (values: { qps: number }) => {
    if (!superAdmin || loading || saving || !snapshot || loadError) return;
    setSaving(true);
    setSavingScope('global');
    try {
      await client.put('/admin/rate-limits', {
        global: { qps: Number(values.qps) },
      });
      message.success(t('ratelimits.global_updated', 'Global rate limit updated'));
      fetchRateLimits();
    } catch (err: any) {
      message.error(err?.userMessage || t('common.error', 'Failed to update global rate limit'));
    } finally {
      setSaving(false);
      setSavingScope(null);
    }
  };

  const handleSaveOrg = async (values: { organization_id: number; qps: number }) => {
    if (
      loading ||
      saving ||
      !snapshot ||
      loadError ||
      !selectedOrgId ||
      Number(values.organization_id) !== selectedOrgId
    )
      return;
    setSaving(true);
    setSavingScope('org');
    try {
      await client.put('/admin/rate-limits', {
        organizations: [
          {
            organization_id: Number(values.organization_id),
            qps: Number(values.qps),
          },
        ],
      });
      message.success(
        t('ux.org_limits_updated').replace(
          '{name}',
          organizations.find(org => org.id === values.organization_id)?.name ||
            t('ux.name_unavailable'),
        ),
      );
      fetchRateLimits(values.organization_id, selectedKeyId);
    } catch (err: any) {
      message.error(
        err?.userMessage || t('common.error', 'Failed to update organization rate limit'),
      );
    } finally {
      setSaving(false);
      setSavingScope(null);
    }
  };

  const handleSaveKey = async (values: {
    key_id: number;
    qps: number;
    quota_5h?: number | null;
    quota_1d?: number | null;
    quota_7d?: number | null;
  }) => {
    if (
      !superAdmin ||
      loading ||
      saving ||
      !snapshot ||
      loadError ||
      !selectedKeyId ||
      Number(values.key_id) !== selectedKeyId
    )
      return;
    setSaving(true);
    setSavingScope('key');
    try {
      const payload: any = {
        key_id: Number(values.key_id),
        qps: Number(values.qps),
      };
      if (values.quota_5h !== undefined && values.quota_5h !== null) {
        payload.quota_5h = Number(values.quota_5h);
      }
      if (values.quota_1d !== undefined && values.quota_1d !== null) {
        payload.quota_1d = Number(values.quota_1d);
      }
      if (values.quota_7d !== undefined && values.quota_7d !== null) {
        payload.quota_7d = Number(values.quota_7d);
      }

      await client.put('/admin/rate-limits', {
        keys: [payload],
      });
      message.success(
        t('ux.key_limits_updated').replace(
          '{name}',
          keys.find(key => key.id === values.key_id)?.name || t('ux.name_unavailable'),
        ),
      );
      fetchRateLimits(selectedOrgId, values.key_id);
    } catch (err: any) {
      message.error(err?.userMessage || t('common.error', 'Failed to update API Key rate limit'));
    } finally {
      setSaving(false);
      setSavingScope(null);
    }
  };

  return (
    <Card
      aria-busy={loading}
      title={t('ratelimits.title', 'Rate Limits & Rolling Quotas')}
      extra={
        <RefreshButton
          onClick={() => {
            fetchLookups();
            setKeysRefreshVersion(version => version + 1);
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
      </div>

      <Row gutter={[24, 24]}>
        {/* Global Rate Limit */}
        <Col xs={24} md={12}>
          <Card
            type="inner"
            title={
              <Space>
                <GlobalOutlined />
                <span>{t('ratelimits.global_card_title', 'Global Gateway QPS')}</span>
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
                  label={t('ratelimits.configured_global_qps', 'Configured Global QPS')}
                >
                  <Tag color="blue">{`${snapshot?.global?.qps ?? 0} req/s`}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label={t('ratelimits.current_live_qps', 'Current Live QPS')}>
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
                disabled={!superAdmin || loadError}
              >
                <Form.Item
                  name="qps"
                  label={t('ratelimits.global_qps_label', 'Global QPS Cap (0 = unlimited)')}
                  rules={[
                    {
                      required: true,
                      message: t('ratelimits.enter_global_qps', 'Enter global QPS'),
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
                    addonAfter="req / sec"
                  />
                </Form.Item>
                <div style={{ marginTop: 8 }}>
                  <Button
                    type="primary"
                    htmlType="submit"
                    icon={<SaveOutlined />}
                    loading={savingScope === 'global'}
                    disabled={!superAdmin || loadError}
                  >
                    {t('ratelimits.save_global_btn', 'Save Global Limit')}
                  </Button>
                </div>
              </Form>
            </div>
          </Card>
        </Col>

        {/* Organization Rate Limit */}
        <Col xs={24} md={12}>
          <Card
            type="inner"
            title={
              <Space>
                <ApartmentOutlined />
                <span>{t('ratelimits.org_card_title', 'Organization QPS')}</span>
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
                    organizationsError || (!organizationsLoading && organizations.length === 0)
                  }
                  onChange={val => {
                    const cachedOrgKeys = val ? keysCache.get(val) : undefined;
                    setSelectedOrgId(val);
                    setSelectedKeyId(undefined);
                    setKeys(cachedOrgKeys ?? []);
                    setKeysLoading(!cachedOrgKeys && Boolean(val));
                    setKeysError(false);
                    keyForm.resetFields();
                    orgForm.setFieldValue('organization_id', val);
                  }}
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

              <Form
                name="organization-limit"
                form={orgForm}
                layout="vertical"
                initialValues={{
                  organization_id: selectedOrgId,
                  qps: snapshot?.organization?.qps ?? 0,
                }}
                onFinish={handleSaveOrg}
                disabled={
                  loadError || organizationsError || (!organizationsLoading && !selectedOrgId)
                }
              >
                <Form.Item name="organization_id" hidden>
                  <InputNumber />
                </Form.Item>
                <Form.Item
                  name="qps"
                  label={t('ratelimits.org_qps_label', 'Organization QPS Limit')}
                  rules={[
                    {
                      required: true,
                      message: t('ratelimits.enter_org_qps', 'Enter organization QPS'),
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
                    addonAfter="req / sec"
                  />
                </Form.Item>
                <div style={{ marginTop: 8 }}>
                  <Button
                    type="primary"
                    htmlType="submit"
                    icon={<SaveOutlined />}
                    loading={savingScope === 'org'}
                    disabled={
                      loadError || organizationsError || (!organizationsLoading && !selectedOrgId)
                    }
                  >
                    {t('ratelimits.save_org_btn', 'Save Organization Limit')}
                  </Button>
                </div>
              </Form>
            </div>
          </Card>
        </Col>
      </Row>

      {/* API Key Rate Limit & Rolling Quotas */}
      <Row gutter={[24, 24]} style={{ marginTop: 24 }}>
        <Col span={24}>
          <Card
            type="inner"
            title={
              <Space>
                <KeyOutlined />
                <span>{t('ratelimits.key_card_title', 'API Key QPS & Token Quotas')}</span>
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
                disabled={keysError || (!isKeysPending && (!selectedOrgId || keys.length === 0))}
                onChange={val => {
                  setSelectedKeyId(val);
                  keyForm.setFieldValue('key_id', val);
                }}
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
                      <Text strong ellipsis style={{ maxWidth: 240, minWidth: 0, marginRight: 12 }}>
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
                                color: k.status === 'disabled' ? 'var(--silver)' : 'var(--success)',
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
                      setKeysRefreshVersion(version => version + 1);
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

            <PageHelp note="ux.independent_save" />
            {!superAdmin && (
              <Alert type="info" message={t('ux.read_only_limits')} style={{ marginBottom: 16 }} />
            )}
            <Form
              name="key-limit"
              form={keyForm}
              layout="vertical"
              onFinish={handleSaveKey}
              disabled={!superAdmin || loadError || !selectedKeyId}
            >
              <Form.Item name="key_id" hidden>
                <InputNumber />
              </Form.Item>

              <Row gutter={[20, 16]}>
                <Col xs={24} sm={12} lg={6}>
                  <Form.Item
                    name="qps"
                    label={t('ratelimits.key_qps_label', 'Key QPS Limit')}
                    rules={[
                      { required: true, message: t('ratelimits.enter_global_qps', 'Enter QPS') },
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
                      addonAfter="req/s"
                    />
                  </Form.Item>
                </Col>
                <Col xs={24} sm={12} lg={6}>
                  <Form.Item
                    name="quota_5h"
                    label={t('ratelimits.quota_5h_label', '5-Hour Rolling Token Quota (optional)')}
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
                    label={t('ratelimits.quota_1d_label', '24-Hour Rolling Token Quota (optional)')}
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
                  disabled={!superAdmin || loadError || !selectedKeyId}
                >
                  {t('ratelimits.save_key_btn', 'Save Key Limits & Quotas')}
                </Button>
              </div>
            </Form>
          </Card>
        </Col>
      </Row>
    </Card>
  );
};
export default RateLimitsPage;
