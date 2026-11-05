import { reportConfigurationError } from '../diagnostics/reportConfigurationError';
import { TableAction, TableActionMenu } from '../common/TableAction';
import { RecordTitle } from '../common/RecordTitle';
import { ListSummary } from '../common/ListSummary';
import { LoadError, StatusTag } from '../common/ConsoleUX';
import { SearchInput } from '../common/SearchInput';
import React, { useState, useEffect, useRef } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useFirstKeyAction } from '../onboarding/useFirstKeyAction';
import { invalidateFirstKeySetup, loadSetupPages } from '../onboarding/setupQueries';
import {
  Card,
  Table,
  Button,
  Space,
  Modal,
  Form,
  Input,
  Select,
  message,
  Typography,
  InputNumber,
  Row,
  Col,
  Tooltip,
  Divider,
  Alert,
} from 'antd';
import {
  PlusOutlined,
  CopyOutlined,
  EditOutlined,
  StopOutlined,
  MinusCircleOutlined,
  QuestionCircleOutlined,
} from '@ant-design/icons';
import { useQuery, useQueries, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
const { Text } = Typography;

interface Organization {
  id: number;
  name: string;
  description: string;
  status: 'active' | 'disabled';
  allowed_provider_ids?: number[];
  allowed_models?: string[];
  has_active_bindings?: boolean;
  has_provider_bindings?: boolean;
  created_at?: string;
}

interface ProviderBindingItem {
  account_id: number;
  priority: number;
  model_allowlist: string[];
  status: string;
}

interface ProviderModelOption {
  model_name: string;
}

interface ProviderOption {
  id: number;
  name: string;
  platform: string;
}

interface ProviderModelSelectProps {
  fieldName: number;
  providers: any[];
  enabled: boolean;
  value?: string[];
  onChange?: (value: string[]) => void;
}

const ProviderModelSelect: React.FC<ProviderModelSelectProps> = ({
  fieldName,
  providers,
  enabled,
  value,
  onChange,
}) => {
  const { t } = useI18n();
  const form = Form.useFormInstance();
  const providerId = Form.useWatch(['bindings', fieldName, 'account_id'], form) as
    number | undefined;
  const { data: models = [], isLoading } = useQuery({
    queryKey: ['provider-models', providerId, 'active'],
    queryFn: async () => {
      const res = await apiClient.get(`/admin/providers/${providerId}/models`, {
        params: { page: 1, page_size: 100, status: 'active' },
      });
      return (res.data?.data?.items || []) as ProviderModelOption[];
    },
    enabled: enabled && !!providerId,
    retry: false,
  });

  const provider = providers.find(item => item.id === providerId);
  const channelLabel = provider
    ? `${provider.name} (${provider.platform})`
    : providerId
      ? t('ux.unnamed_provider')
      : '';
  const options = models.map(model => ({
    label: `${model.model_name} — ${channelLabel}`,
    value: model.model_name,
  }));
  const availableModelNames = new Set(options.map(option => option.value));
  (value || []).forEach(modelName => {
    if (!availableModelNames.has(modelName)) {
      options.push({ label: modelName, value: modelName });
    }
  });

  const handleSelectAll = (e?: React.MouseEvent) => {
    e?.preventDefault();
    e?.stopPropagation();
    const allValues = options.map(opt => opt.value);
    onChange?.(allValues);
    form.setFieldValue(['bindings', fieldName, 'model_allowlist'], allValues);
  };

  const handleClear = (e?: React.MouseEvent) => {
    e?.preventDefault();
    e?.stopPropagation();
    onChange?.([]);
    form.setFieldValue(['bindings', fieldName, 'model_allowlist'], []);
  };

  return (
    <div>
      <Select
        mode="multiple"
        showSearch
        optionFilterProp="label"
        loading={isLoading}
        options={options}
        maxTagCount="responsive"
        value={value}
        onChange={onChange}
        placeholder={t(
          'organizations.binding_models_placeholder',
          'Select models from this channel',
        )}
        popupRender={menu => (
          <div>
            {options.length > 0 && (
              <div
                style={{
                  padding: '6px 12px',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  borderBottom: '1px solid #f0f0f0',
                  background: '#fafafa',
                }}
                onMouseDown={e => e.preventDefault()}
              >
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {t('organizations.selected_models_count', '已选 {count} / {total} 个模型')
                    .replace('{count}', String((value || []).length))
                    .replace('{total}', String(options.length))}
                </Text>
                <Space size={8}>
                  <Button
                    type="link"
                    size="small"
                    style={{ padding: 0, height: 'auto', fontSize: 12 }}
                    onClick={handleSelectAll}
                  >
                    {t('organizations.select_all_models', '全选模型')}
                  </Button>
                  <Divider type="vertical" style={{ margin: 0 }} />
                  <Button
                    type="link"
                    size="small"
                    style={{ padding: 0, height: 'auto', fontSize: 12 }}
                    onClick={handleClear}
                  >
                    {t('organizations.clear_all_models', '清空')}
                  </Button>
                </Space>
              </div>
            )}
            {menu}
          </div>
        )}
      />
      {options.length > 0 && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 4 }}>
          <Space size={8}>
            <Button
              type="link"
              size="small"
              style={{ padding: 0, height: 'auto', fontSize: 12 }}
              onClick={handleSelectAll}
            >
              {t('organizations.select_all_models', '全选模型')}
            </Button>
            <Divider type="vertical" style={{ margin: 0 }} />
            <Button
              type="link"
              size="small"
              style={{ padding: 0, height: 'auto', fontSize: 12 }}
              onClick={handleClear}
            >
              {t('organizations.clear_all_models', '清空')}
            </Button>
          </Space>
        </div>
      )}
    </div>
  );
};

const ProviderBindingsFields: React.FC<{ providers: any[]; enabled: boolean }> = ({
  providers,
  enabled,
}) => {
  const { t } = useI18n();
  const form = Form.useFormInstance();
  return (
    <Form.List name="bindings">
      {(fields, { add, remove }) => (
        <>
          {fields.map(({ key, name, ...restField }) => (
            <div
              key={key}
              style={{ marginBottom: 16, paddingBottom: 8, borderBottom: '1px solid #f0f0f0' }}
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  marginBottom: 12,
                }}
              >
                <Text strong>
                  {t('organizations.binding_item_title', 'Binding {number}').replace(
                    '{number}',
                    String(name + 1),
                  )}
                </Text>
                <Tooltip title={t('organizations.remove_binding', 'Remove binding')}>
                  <Button
                    type="text"
                    size="small"
                    danger
                    icon={<MinusCircleOutlined />}
                    aria-label={t('organizations.remove_binding', 'Remove binding')}
                    onClick={() => remove(name)}
                  />
                </Tooltip>
              </div>
              <Row gutter={[16, 0]}>
                <Col xs={24} sm={12} lg={8}>
                  <Form.Item
                    {...restField}
                    name={[name, 'account_id']}
                    label={t('organizations.binding_provider_label', 'Upstream provider')}
                    tooltip={{
                      title: t(
                        'organizations.binding_provider_help',
                        'Select the provider account to make available to this organization. Its credentials and models come from provider settings.',
                      ),
                      icon: <QuestionCircleOutlined style={{ cursor: 'default' }} />,
                    }}
                    rules={[
                      {
                        required: true,
                        message: t('organizations.provider_required', 'Provider required'),
                      },
                    ]}
                  >
                    <Select
                      placeholder={t('organizations.select_provider', 'Select Provider')}
                      options={providers?.map((p: any) => ({
                        label: p.name,
                        value: p.id,
                      }))}
                      onChange={providerId => {
                        const previousProviderId = form.getFieldValue([
                          'bindings',
                          name,
                          'account_id',
                        ]);
                        if (previousProviderId !== providerId) {
                          form.setFieldValue(['bindings', name, 'model_allowlist'], []);
                        }
                      }}
                    />
                  </Form.Item>
                </Col>
                <Col xs={12} sm={12} lg={4}>
                  <Form.Item
                    {...restField}
                    name={[name, 'priority']}
                    initialValue={0}
                    label={t('organizations.binding_priority_label', 'Binding priority')}
                    tooltip={{
                      title: t(
                        'organizations.binding_priority_help',
                        'When a model is available from multiple providers, routing prefers the binding with the higher priority and continues to the next binding if the model is unavailable.',
                      ),
                      icon: <QuestionCircleOutlined style={{ cursor: 'default' }} />,
                    }}
                  >
                    <InputNumber
                      min={-10000}
                      max={10000}
                      precision={0}
                      style={{ width: '100%' }}
                      placeholder={t('providers.priority_label', 'Priority')}
                    />
                  </Form.Item>
                </Col>
                <Col xs={12} sm={12} lg={4}>
                  <Form.Item
                    {...restField}
                    name={[name, 'status']}
                    initialValue="active"
                    label={t('organizations.binding_status_label', 'Binding status')}
                    tooltip={{
                      title: t(
                        'organizations.binding_status_help',
                        'Enable or pause this binding for the organization. The provider account itself is unchanged.',
                      ),
                      icon: <QuestionCircleOutlined style={{ cursor: 'default' }} />,
                    }}
                  >
                    <Select
                      options={[
                        {
                          label: t('organizations.binding_status_active', 'Enabled'),
                          value: 'active',
                        },
                        {
                          label: t('organizations.binding_status_disabled', 'Paused'),
                          value: 'disabled',
                        },
                      ]}
                    />
                  </Form.Item>
                </Col>
                <Col xs={24} sm={12} lg={8}>
                  <Form.Item
                    {...restField}
                    name={[name, 'model_allowlist']}
                    label={t('organizations.binding_models_label', 'Model allowlist')}
                    tooltip={{
                      title: t(
                        'organizations.binding_models_help',
                        "Select from this provider's active models. Each option shows the model ID and channel. Leave empty for no additional filter.",
                      ),
                      icon: <QuestionCircleOutlined style={{ cursor: 'default' }} />,
                    }}
                  >
                    <ProviderModelSelect fieldName={name} providers={providers} enabled={enabled} />
                  </Form.Item>
                </Col>
              </Row>
            </div>
          ))}
          <Button
            type="dashed"
            onClick={() => add({ status: 'active', priority: 0, model_allowlist: [] })}
            block
            icon={<PlusOutlined />}
          >
            {t('organizations.add_binding', 'Add Provider Binding')}
          </Button>
        </>
      )}
    </Form.List>
  );
};

export const OrganizationsPage: React.FC = () => {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const savingOrg = useRef(false);
  const savingBindings = useRef(false);
  const invalidateOrganizationQueries = () => {
    invalidateFirstKeySetup(queryClient);
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['organizations'] }),
      queryClient.invalidateQueries({ queryKey: ['organizations-select'] }),
      queryClient.invalidateQueries({ queryKey: ['ux-organizations'] }),
      queryClient.invalidateQueries({
        predicate: query =>
          query.queryKey[0] === 'resource-name' && query.queryKey[4] === 'organizations',
      }),
    ]);
  };
  const [searchParams, setSearchParams] = useSearchParams();
  const [modalVisible, setModalVisible] = useState(false);
  const [bindingModalVisible, setBindingModalVisible] = useState(false);
  const [duplicatingOrg, setDuplicatingOrg] = useState(false);
  const [editingOrg, setEditingOrg] = useState<Organization | null>(null);
  const [modelsOrg, setModelsOrg] = useState<Organization | null>(null);
  const [modelSearch, setModelSearch] = useState('');
  const [bindingOrg, setBindingOrg] = useState<Organization | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState(() => searchParams.get('search') || '');

  useEffect(() => {
    const q = searchParams.get('search') || '';
    setSearch(q);
    setPage(1);
  }, [searchParams]);

  const [form] = Form.useForm();
  const [bindingForm] = Form.useForm();
  const selectedProviderIds = Form.useWatch('allowed_provider_ids', form) as number[] | undefined;
  const selectedModels = Form.useWatch('allowed_models', form) as string[] | undefined;

  const currentUser = (() => {
    try {
      const raw = localStorage.getItem('user_info');
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  })();
  const isPlatformAdmin = currentUser?.role === 'super_admin';

  const {
    data: orgData,
    isLoading,
    isError,
    refetch,
  } = useQuery({
    queryKey: ['organizations', page, pageSize, search],
    queryFn: async () => {
      const res = await apiClient.get('/admin/organizations', {
        params: { page, page_size: pageSize, search: search || undefined },
      });
      return res.data?.data || { items: [], total: 0 };
    },
  });

  const {
    data: providersData,
    isError: providersError,
    refetch: refetchProviders,
  } = useQuery({
    queryKey: ['providers-list', currentUser?.id, currentUser?.role, currentUser?.organization_id],
    queryFn: async ({ signal }) => {
      const providers = await loadSetupPages<ProviderOption>('/admin/providers', signal);
      return providers;
    },
    enabled: isPlatformAdmin || currentUser?.role === 'org_admin',
    retry: false,
  });

  const modelProviderIds = Array.from(new Set(selectedProviderIds || [])).sort((a, b) => a - b);

  const providerModelQueries = useQueries({
    queries: modelProviderIds.map(providerId => ({
      queryKey: ['provider-models', providerId, 'active'],
      queryFn: async () => {
        const res = await apiClient.get(`/admin/providers/${providerId}/models`, {
          params: { page: 1, page_size: 100, status: 'active' },
        });
        return (res.data?.data?.items || []) as ProviderModelOption[];
      },
      enabled: isPlatformAdmin && modalVisible,
      retry: false,
    })),
  });

  const modelOptionsByName = new Map<string, string[]>();
  providerModelQueries.forEach((query, index) => {
    const provider = providersData?.find((item: any) => item.id === modelProviderIds[index]);
    if (!provider || !query.data) return;
    query.data.forEach(model => {
      const channel = `${provider.name} (${provider.platform})`;
      const channels = modelOptionsByName.get(model.model_name) || [];
      if (!channels.includes(channel)) channels.push(channel);
      modelOptionsByName.set(model.model_name, channels);
    });
  });

  const modelOptions = Array.from(modelOptionsByName.entries())
    .sort(([modelA], [modelB]) => modelA.localeCompare(modelB))
    .map(([modelName, channels]) => ({
      label: `${modelName} — ${channels.join(', ')}`,
      value: modelName,
    }));
  const availableModelNames = new Set(modelOptions.map(option => option.value));
  (selectedModels || []).forEach(modelName => {
    if (!availableModelNames.has(modelName)) {
      modelOptions.push({ label: modelName, value: modelName });
    }
  });

  const saveMutation = useMutation({
    mutationFn: async (values: any) => {
      const payload = {
        name: values.name,
        description: values.description,
        status: values.status,
        allowed_provider_ids: duplicatingOrg ? [] : values.allowed_provider_ids || [],
        allowed_models: duplicatingOrg ? [] : values.allowed_models || [],
        bindings: duplicatingOrg ? values.bindings || [] : undefined,
      };
      if (editingOrg) {
        return apiClient.put(`/admin/organizations/${editingOrg.id}`, payload);
      }
      return apiClient.post('/admin/organizations', payload);
    },
    onSuccess: response => {
      message.success(t('organizations.saved_success', 'Organization saved successfully'));
      setModalVisible(false);
      form.resetFields();
      invalidateOrganizationQueries();
      if (!editingOrg && searchParams.get('onboarding') === 'first-key') {
        const id = response.data?.data?.id;
        if (id) {
          const next = new URLSearchParams(searchParams);
          next.set('setup_org', String(id));
          setSearchParams(next, { replace: true });
        }
      }
    },
    onSettled: () => {
      savingOrg.current = false;
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      return apiClient.delete(`/admin/organizations/${id}`);
    },
    onSuccess: () => {
      message.success(t('organizations.disabled_success', 'Organization disabled successfully'));
      invalidateOrganizationQueries();
    },
  });

  const bindingMutation = useMutation({
    mutationFn: async ({ orgId, bindings }: { orgId: number; bindings: ProviderBindingItem[] }) => {
      return apiClient.put(`/admin/organizations/${orgId}/provider-bindings`, { bindings });
    },
    onSuccess: () => {
      message.success(
        t('organizations.bindings_updated', 'Provider bindings updated successfully'),
      );
      setBindingModalVisible(false);
      bindingForm.resetFields();
      invalidateOrganizationQueries();
    },
    onSettled: () => {
      savingBindings.current = false;
    },
  });

  const loadBindingsMutation = useMutation({
    mutationFn: async (record: Organization) => {
      const res = await apiClient.get(`/admin/organizations/${record.id}/provider-bindings`);
      const items = res.data?.data?.items ?? res.data?.data?.bindings;
      if (!Array.isArray(items)) throw new Error('Invalid bindings response');
      return { record, items: items as ProviderBindingItem[] };
    },
    onSuccess: ({ record, items }) => {
      setBindingOrg(record);
      bindingForm.resetFields();
      bindingForm.setFieldsValue({ bindings: items });
      setBindingModalVisible(true);
    },
    onError: (error: any) => {
      if (error?.configurationFeedbackHandled) return;
      reportConfigurationError('unknown_error');
    },
  });

  const loadDuplicateMutation = useMutation({
    mutationFn: async (record: Organization) => {
      const [organization, bindings] = await Promise.all([
        apiClient.get(`/admin/organizations/${record.id}`),
        apiClient.get(`/admin/organizations/${record.id}/provider-bindings`),
      ]);
      const items = bindings.data?.data?.items ?? bindings.data?.data?.bindings;
      if (!Array.isArray(items)) throw new Error('Invalid bindings response');
      return { source: organization.data.data as Organization, items };
    },
    onSuccess: ({ source, items }) => {
      setEditingOrg(null);
      setDuplicatingOrg(true);
      form.resetFields();
      form.setFieldsValue({ ...source, name: `${source.name} - Copy`, bindings: items });
      setModalVisible(true);
    },
  });

  const handleEdit = (record: Organization) => {
    if (savingOrg.current) return;
    setDuplicatingOrg(false);
    setEditingOrg(record);
    form.setFieldsValue({
      name: record.name,
      description: record.description,
      status: record.status,
      allowed_provider_ids: record.allowed_provider_ids || [],
      allowed_models: record.allowed_models || [],
    });
    setModalVisible(true);
  };

  const handleAdd = () => {
    if (savingOrg.current) return;
    setDuplicatingOrg(false);
    setEditingOrg(null);
    form.resetFields();
    form.setFieldsValue({ status: 'active', allowed_provider_ids: [], allowed_models: [] });
    setModalVisible(true);
  };

  useFirstKeyAction(['create-organization', 'edit-organization'], isPlatformAdmin, (action, id) => {
    if (action === 'create-organization') handleAdd();
    else if (id) {
      void apiClient
        .get(`/admin/organizations/${id}`)
        .then(response => {
          handleEdit(response.data.data);
        })
        .catch(() => {});
    }
  });

  const handleOpenBindings = (record: Organization) => {
    if (savingBindings.current) return;
    loadBindingsMutation.mutate(record);
  };

  const columns = [
    {
      title: t('organizations.col_name', 'Organization Name'),
      dataIndex: 'name',
      key: 'name',
      width: 230,
      render: (val: string, record: Organization) => (
        <RecordTitle name={val} description={record.description} />
      ),
    },
    {
      title: t('common.status', 'Status'),
      dataIndex: 'status',
      key: 'status',
      width: 90,
      render: (val: string) => <StatusTag value={val} />,
    },
    {
      title: t('organizations.col_allowed_providers', 'Allowed Providers'),
      dataIndex: 'allowed_provider_ids',
      key: 'allowed_provider_ids',
      width: 190,
      render: (list: number[]) => {
        if (list?.length && !providersData) {
          return (
            <Text type="secondary">
              {t(providersError ? 'organizations.provider_names_load_error' : 'common.loading')}
            </Text>
          );
        }
        return (
          <ListSummary
            items={(list || []).map(
              id =>
                providersData?.find(provider => provider.id === id)?.name ||
                t(
                  providersError
                    ? 'organizations.provider_names_load_error'
                    : 'ux.unnamed_provider',
                ),
            )}
            empty={t('organizations.no_available_providers', 'No assigned public channels')}
          />
        );
      },
    },
    {
      title: t('organizations.col_allowed_models', 'Allowed Models'),
      dataIndex: 'allowed_models',
      key: 'allowed_models',
      width: 220,
      render: (list: string[], record: Organization) => (
        <ListSummary
          items={list || []}
          limit={1}
          empty={t('organizations.no_available_models', 'No assigned public models')}
          onMore={() => {
            setModelsOrg(record);
            setModelSearch('');
          }}
        />
      ),
    },
    {
      title: t('common.actions', 'Actions'),
      key: 'actions',
      width: isPlatformAdmin ? 286 : 140,
      fixed: 'right' as const,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, record: Organization) => {
        const canEdit = isPlatformAdmin;

        return (
          <Space size={4} wrap={false} style={{ whiteSpace: 'nowrap' }}>
            {(isPlatformAdmin ||
              (currentUser?.role === 'org_admin' && currentUser.organization_id === record.id)) && (
              <Link to={`/admin/rate-limits?organization_id=${record.id}`}>
                {t(
                  isPlatformAdmin
                    ? 'ratelimits.org_limits_link'
                    : 'ratelimits.org_view_limits_link',
                )}
              </Link>
            )}
            {isPlatformAdmin && (
              <TableAction
                size="small"
                onClick={() => handleOpenBindings(record)}
                loading={
                  loadBindingsMutation.isPending && loadBindingsMutation.variables?.id === record.id
                }
                disabled={loadBindingsMutation.isPending}
              >
                {t('organizations.btn_bindings', 'Bindings')}
              </TableAction>
            )}
            {canEdit && (
              <TableAction
                label={t('common.edit', 'Edit')}
                icon={<EditOutlined />}
                onClick={() => handleEdit(record)}
              />
            )}
            {isPlatformAdmin && (
              <TableAction
                label={t('organizations.btn_duplicate')}
                icon={<CopyOutlined />}
                disabled={loadDuplicateMutation.isPending || saveMutation.isPending}
                loading={
                  loadDuplicateMutation.isPending &&
                  loadDuplicateMutation.variables?.id === record.id
                }
                onClick={() => loadDuplicateMutation.mutate(record)}
              />
            )}
            {isPlatformAdmin && (
              <TableActionMenu
                menu={{
                  items: [
                    {
                      key: 'disable',
                      label: t('keys.btn_disable', 'Disable'),
                      icon: <StopOutlined />,
                      danger: true,
                      disabled: deleteMutation.isPending,
                      onClick: () =>
                        Modal.confirm({
                          title: t('organizations.disable_confirm', 'Disable organization?'),
                          content: t(
                            'organizations.disable_confirm_desc',
                            'All API keys belonging to this organization will immediately be rejected.',
                          ),
                          okText: t('keys.btn_disable', 'Disable'),
                          cancelText: t('common.cancel'),
                          okButtonProps: { danger: true },
                          onOk: () => deleteMutation.mutateAsync(record.id),
                        }),
                    },
                  ],
                }}
              />
            )}
          </Space>
        );
      },
    },
  ];

  return (
    <Card
      title={t('organizations.title', 'Enterprise Organizations & Tenant Scope')}
      extra={
        <Space>
          <SearchInput
            placeholder={t('common.search', 'Search organizations...')}
            allowClear
            value={search}
            onChange={e => {
              setSearch(e.target.value);
              if (!e.target.value) {
                setPage(1);
                setSearchParams({});
              }
            }}
            onSearch={v => {
              setSearch(v);
              setPage(1);
              setSearchParams(v ? { search: v } : {});
            }}
            style={{ width: 220 }}
          />
          {isPlatformAdmin && (
            <Button type="primary" icon={<PlusOutlined />} onClick={handleAdd}>
              {t('organizations.create_org', 'New Organization')}
            </Button>
          )}
        </Space>
      }
    >
      {isError && <LoadError retry={refetch} />}
      {providersError && (
        <Alert
          className="console-load-error"
          type="error"
          showIcon
          message={t('organizations.provider_names_load_error')}
          action={<Button onClick={() => void refetchProviders()}>{t('common.retry')}</Button>}
        />
      )}
      <Table
        rowKey="id"
        dataSource={orgData?.items || []}
        columns={columns}
        loading={isLoading}
        tableLayout="fixed"
        scroll={{ x: 900 }}
        pagination={{
          current: page,
          pageSize,
          total: orgData?.total || 0,
          onChange: (p, ps) => {
            setPage(p);
            setPageSize(ps);
          },
        }}
      />

      <Modal
        title={`${modelsOrg?.name || ''} · ${t('organizations.col_allowed_models')}`}
        open={!!modelsOrg}
        onCancel={() => setModelsOrg(null)}
        footer={null}
        width={640}
        destroyOnHidden
      >
        <SearchInput
          aria-label={t('common.search')}
          placeholder={t('common.search')}
          value={modelSearch}
          onChange={event => setModelSearch(event.target.value)}
          allowClear
          style={{ marginBottom: 16 }}
        />
        <div className="model-list-dialog">
          {(modelsOrg?.allowed_models || [])
            .filter(model => model.toLowerCase().includes(modelSearch.toLowerCase()))
            .map(model => (
              <code key={model}>{model}</code>
            ))}
          {modelsOrg &&
            !modelsOrg.allowed_models?.some(model =>
              model.toLowerCase().includes(modelSearch.toLowerCase()),
            ) && <Text type="secondary">{t('ux.no_data')}</Text>}
        </div>
      </Modal>

      {/* Edit / Create Org Modal */}
      <Modal
        title={
          duplicatingOrg
            ? t('organizations.btn_duplicate')
            : editingOrg
              ? `${t('organizations.edit_org', 'Edit Organization')} · ${editingOrg.name}`
              : t('organizations.create_org', 'Create Organization')
        }
        open={modalVisible}
        width={duplicatingOrg ? 880 : 520}
        okText={editingOrg ? t('common.save') : t('common.create')}
        cancelText={t('common.cancel')}
        maskClosable={false}
        onOk={() => form.submit()}
        onCancel={() => {
          if (!savingOrg.current) setModalVisible(false);
        }}
        confirmLoading={saveMutation.isPending}
      >
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          form={form}
          layout="vertical"
          disabled={saveMutation.isPending}
          onFinish={values => {
            if (savingOrg.current) return;
            savingOrg.current = true;
            saveMutation.mutate(values);
          }}
        >
          <Form.Item
            name="name"
            label={t('organizations.col_name', 'Organization Name')}
            rules={[
              {
                required: true,
                message: t('organizations.name_required', 'Please enter organization name'),
              },
            ]}
          >
            <Input
              placeholder={t('organizations.name_placeholder', 'e.g. Research-Team, Production-AI')}
            />
          </Form.Item>
          <Form.Item name="description" label={t('common.description', 'Description')}>
            <Input.TextArea
              placeholder={t(
                'organizations.description_placeholder',
                'Optional organization description',
              )}
              rows={2}
            />
          </Form.Item>
          {isPlatformAdmin && (
            <>
              <Form.Item
                name="status"
                label={t('common.status', 'Status')}
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { label: t('common.active', 'Active'), value: 'active' },
                    { label: t('common.disabled', 'Disabled'), value: 'disabled' },
                  ]}
                />
              </Form.Item>
              <Form.Item
                hidden={duplicatingOrg}
                name="allowed_provider_ids"
                label={t('organizations.col_allowed_providers', 'Allowed Providers')}
                extra={t(
                  'organizations.binding_policy_help',
                  'Saving channels and models synchronizes provider bindings. Saving bindings also updates this organization.',
                )}
              >
                <Select
                  mode="multiple"
                  placeholder={t(
                    'organizations.allowed_providers_placeholder',
                    'Select public channels to assign',
                  )}
                  options={providersData?.map((p: any) => ({
                    label: `${p.name} (${p.platform})`,
                    value: p.id,
                  }))}
                />
              </Form.Item>
            </>
          )}
          <Form.Item
            hidden={duplicatingOrg}
            name="allowed_models"
            label={t('organizations.col_allowed_models', 'Allowed Models')}
            extra={
              <div>
                <div>
                  {t(
                    'organizations.allowed_models_help',
                    'Select channels above to see their active models. Each option shows the model and channel.',
                  )}
                </div>
                {modelOptions.length > 0 && (
                  <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 4 }}>
                    <Space size={8}>
                      <Button
                        type="link"
                        size="small"
                        style={{ padding: 0, height: 'auto', fontSize: 12 }}
                        onClick={() => {
                          form.setFieldValue(
                            'allowed_models',
                            modelOptions.map(opt => opt.value),
                          );
                        }}
                      >
                        {t('organizations.select_all_models', '全选模型')}
                      </Button>
                      <Divider type="vertical" style={{ margin: 0 }} />
                      <Button
                        type="link"
                        size="small"
                        style={{ padding: 0, height: 'auto', fontSize: 12 }}
                        onClick={() => {
                          form.setFieldValue('allowed_models', []);
                        }}
                      >
                        {t('organizations.clear_all_models', '清空')}
                      </Button>
                    </Space>
                  </div>
                )}
              </div>
            }
          >
            <Select
              mode="multiple"
              showSearch
              optionFilterProp="label"
              loading={providerModelQueries.some(query => query.isLoading)}
              options={modelOptions}
              maxTagCount="responsive"
              placeholder={t(
                'organizations.allowed_models_placeholder',
                'Select channels above to choose models; leave empty to allow all',
              )}
              popupRender={menu => (
                <div>
                  {modelOptions.length > 0 && (
                    <div
                      style={{
                        padding: '6px 12px',
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        borderBottom: '1px solid #f0f0f0',
                        background: '#fafafa',
                      }}
                      onMouseDown={e => e.preventDefault()}
                    >
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {t('organizations.selected_models_count', '已选 {count} / {total} 个模型')
                          .replace('{count}', String((selectedModels || []).length))
                          .replace('{total}', String(modelOptions.length))}
                      </Text>
                      <Space size={8}>
                        <Button
                          type="link"
                          size="small"
                          style={{ padding: 0, height: 'auto', fontSize: 12 }}
                          onClick={() => {
                            form.setFieldValue(
                              'allowed_models',
                              modelOptions.map(opt => opt.value),
                            );
                          }}
                        >
                          {t('organizations.select_all_models', '全选模型')}
                        </Button>
                        <Divider type="vertical" style={{ margin: 0 }} />
                        <Button
                          type="link"
                          size="small"
                          style={{ padding: 0, height: 'auto', fontSize: 12 }}
                          onClick={() => {
                            form.setFieldValue('allowed_models', []);
                          }}
                        >
                          {t('organizations.clear_all_models', '清空')}
                        </Button>
                      </Space>
                    </div>
                  )}
                  {menu}
                </div>
              )}
            />
          </Form.Item>
          {duplicatingOrg && (
            <ProviderBindingsFields providers={providersData || []} enabled={modalVisible} />
          )}
        </Form>
      </Modal>

      {/* Provider Bindings Modal */}
      <Modal
        title={`${t('organizations.bindings_modal_title', 'Provider Bindings for')} ${bindingOrg?.name}`}
        open={bindingModalVisible}
        maskClosable={false}
        width={880}
        onOk={() => bindingForm.submit()}
        onCancel={() => {
          if (savingBindings.current) return;
          setBindingModalVisible(false);
          bindingForm.resetFields();
        }}
        confirmLoading={bindingMutation.isPending}
      >
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          form={bindingForm}
          layout="vertical"
          disabled={bindingMutation.isPending}
          onFinish={values => {
            if (bindingOrg && !savingBindings.current) {
              savingBindings.current = true;
              bindingMutation.mutate({
                orgId: bindingOrg.id,
                bindings: values.bindings || [],
              });
            }
          }}
        >
          <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>
            {t(
              'organizations.bindings_desc',
              'Configure provider bindings shared by all API keys in this organization. Requests still specify the model to call.',
            )}
          </Text>
          <ProviderBindingsFields providers={providersData || []} enabled={bindingModalVisible} />
          <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            {t(
              'organizations.bindings_save_help',
              'Saving replaces all bindings and updates organization allowlists. Paused bindings block calls; removing all bindings uses organization policy.',
            )}
          </Text>
        </Form>
      </Modal>
    </Card>
  );
};
