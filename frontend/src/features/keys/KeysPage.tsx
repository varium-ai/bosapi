import { reportConfigurationError } from '../diagnostics/reportConfigurationError';
import { ConsoleToolbar } from '../common/ConsoleToolbar';
import { TableAction, TableActionMenu } from '../common/TableAction';
import { SearchInput } from '../common/SearchInput';
import { compactKey } from '../common/display';
import { ResourceName } from '../common/ResourceName';
import React, { useState, useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useFirstKeyAction } from '../onboarding/useFirstKeyAction';
import { invalidateFirstKeySetup } from '../onboarding/setupQueries';
import {
  Card,
  Table,
  Button,
  Space,
  Modal,
  Form,
  Input,
  Select,
  Popconfirm,
  message,
  Alert,
  Typography,
  InputNumber,
  Row,
  Col,
  Tooltip,
  Divider,
  Switch,
} from 'antd';
import {
  PlusOutlined,
  CopyOutlined,
  DeleteOutlined,
  UploadOutlined,
  ExportOutlined,
  EditOutlined,
  StopOutlined,
  CheckCircleOutlined,
} from '@ant-design/icons';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { AdvancedFields, LoadError, StatusTag, useDiscardChanges } from '../common/ConsoleUX';
import { apiClient } from '../../api/client';
import { adminKeysQueryOptions, organizationOptionsQueryOptions } from '../../api/adminQueries';
import { useI18n } from '../../i18n';
import { type ImportedAPIKey, parseAPIKeyImportFile } from './importKeys';
import { KeyClientImportModal, KeySecretActions } from './KeySecretActions';
import { KeyConnectionInfo, KeyCopyAllButton } from './KeyConnectionInfo';
import { KeyExpiryInput } from './KeyExpiryInput';
import { KeyConfigurationFields } from './KeyConfigurationFields';
import { KeyImportGuide } from './KeyImportGuide';
import { KeyTemplatesModal } from './KeyTemplatesModal';
import {
  getKeyTemplateSettings,
  emptyKeyTemplateSettings,
  keyTemplatesQueryOptions,
} from './keyTemplates';
import { KeyUsageCell } from './KeyUsageCell';
import { KeyAccessFields } from './KeyAccessFields';
import type { APIResponse } from '../../api/types';
import type { APIKeyFormValues, APIKeyRecord } from './types';
import type { KeySecretInfo } from './KeySecretActions';
import type { ColumnsType } from 'antd/es/table';

const { Text, Paragraph } = Typography;

export const KeysPage: React.FC = () => {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [templatesVisible, setTemplatesVisible] = useState(false);
  const [selectedTemplateID, setSelectedTemplateID] = useState<number>();
  const [duplicatingKey, setDuplicatingKey] = useState<APIKeyRecord | null>(null);
  const [createModalVisible, setCreateModalVisible] = useState(false);
  const [importGuideVisible, setImportGuideVisible] = useState(false);
  const [importParsing, setImportParsing] = useState(false);
  const [selectedImportTemplateID, setSelectedImportTemplateID] = useState<number>();
  const importing = React.useRef(false);
  const [importModalVisible, setImportModalVisible] = useState(false);
  const [importedKeys, setImportedKeys] = useState<ImportedAPIKey[]>([]);
  const [importFileName, setImportFileName] = useState('');
  const [editModalVisible, setEditModalVisible] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKeyRecord | null>(null);
  const [newKeyModalVisible, setNewKeyModalVisible] = useState(false);
  const creating = React.useRef(false);
  const [generatedKey, setGeneratedKey] = useState<string>('');
  const [keyMode, setKeyMode] = useState<'generate' | 'existing'>('generate');
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
  const [importTarget, setImportTarget] = useState<KeySecretInfo | null>(null);
  const [varyImportTarget, setVaryImportTarget] = useState<KeySecretInfo | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState(() => searchParams.get('search') || '');
  const [filterOrg, setFilterOrg] = useState<number | undefined>(() => {
    const org = searchParams.get('organization_id') || searchParams.get('org_id');
    return org && Number(org) > 0 ? Number(org) : undefined;
  });

  useEffect(() => {
    const q = searchParams.get('search') || '';
    setSearch(q);
    const org = searchParams.get('organization_id') || searchParams.get('org_id');
    setFilterOrg(org && Number(org) > 0 ? Number(org) : undefined);
    setPage(1);
    setSelectedRowKeys([]);
  }, [searchParams]);

  const [isWideScreen, setIsWideScreen] = useState(() => {
    if (typeof window === 'undefined') return true;
    return window.innerWidth >= 1100;
  });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const mql = window.matchMedia('(min-width: 1100px)');
    const handler = (e: MediaQueryListEvent) => setIsWideScreen(e.matches);
    setIsWideScreen(mql.matches);
    mql.addEventListener('change', handler);
    return () => mql.removeEventListener('change', handler);
  }, []);

  const [form] = Form.useForm<APIKeyFormValues>();
  const [editForm] = Form.useForm<APIKeyFormValues>();
  const editingOrganizationID = Form.useWatch('organization_id', editForm);
  const [importForm] = Form.useForm<APIKeyFormValues>();
  const cancelCreate = useDiscardChanges(form, () => setCreateModalVisible(false));
  const cancelEdit = useDiscardChanges(editForm, () => {
    setEditModalVisible(false);
    setEditingKey(null);
  });

  const currentUser = (() => {
    try {
      const raw = localStorage.getItem('user_info');
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  })();

  const {
    data: keysData,
    isLoading,
    isError,
    refetch,
  } = useQuery(
    adminKeysQueryOptions({
      page,
      pageSize,
      search,
      organizationId: filterOrg,
    }),
  );

  const { data: orgsData } = useQuery(organizationOptionsQueryOptions());
  const {
    data: keyTemplates = [],
    isLoading: templatesLoading,
    isError: templatesError,
    refetch: refetchTemplates,
  } = useQuery({
    ...keyTemplatesQueryOptions(currentUser?.id),
    enabled: createModalVisible || importModalVisible,
  });

  const currentOrgId = currentUser?.organization_id;

  const invalidateKeyQueries = () => {
    invalidateFirstKeySetup(queryClient);
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['admin-keys'] }),
      queryClient.invalidateQueries({ queryKey: ['key-name-options'] }),
      queryClient.invalidateQueries({
        predicate: query => query.queryKey[0] === 'resource-name' && query.queryKey[4] === 'keys',
      }),
    ]);
  };

  const createMutation = useMutation({
    mutationFn: async (values: APIKeyFormValues) => {
      const payload = {
        name: values.name.trim(),
        status: values.status,
        expires_at: values.expires_at || null,
        api_key: keyMode === 'existing' ? values.api_key : undefined,
        organization_id: Number(values.organization_id),
        allowed_provider_ids: values.allowed_provider_ids || [],
        allowed_models: values.allowed_models || [],
        quota_amount: values.quota_amount ?? null,
        quota_tokens: values.quota_tokens ?? null,
        daily_quota_amount: values.daily_quota_amount ?? null,
        weekly_quota_amount: values.weekly_quota_amount ?? null,
        monthly_quota_amount: values.monthly_quota_amount ?? null,
        daily_quota_tokens: values.daily_quota_tokens ?? null,
        weekly_quota_tokens: values.weekly_quota_tokens ?? null,
        monthly_quota_tokens: values.monthly_quota_tokens ?? null,
        rate_limit_5h: values.rate_limit_5h ?? null,
        rate_limit_1d: values.rate_limit_1d ?? null,
        rate_limit_7d: values.rate_limit_7d ?? null,
        ip_allowlist: values.ip_allowlist || [],
        ip_blocklist: values.ip_blocklist || [],
      };
      const res = await apiClient.post('/admin/keys', payload);
      return res.data;
    },
    onSuccess: data => {
      setCreateModalVisible(false);
      form.resetFields();
      invalidateKeyQueries();
      const createdKey = data?.data?.key || data?.key;
      if (createdKey) {
        setGeneratedKey(createdKey);
        setNewKeyModalVisible(true);
      }
    },
    onSettled: () => {
      creating.current = false;
    },
  });

  const loadDuplicateMutation = useMutation({
    mutationFn: async (id: number) =>
      (await apiClient.get<APIResponse<APIKeyRecord>>(`/admin/keys/${id}`)).data.data,
    onSuccess: source => {
      form.resetFields();
      setDuplicatingKey(source);
      setKeyMode('generate');
      form.setFieldsValue({ ...source, name: `${source.name} - Copy` });
      setCreateModalVisible(true);
    },
  });

  const importMutation = useMutation({
    mutationFn: async () => {
      const values = await importForm.validateFields();
      const payload = {
        keys: importedKeys,
        ...getKeyTemplateSettings(values),
      };
      if (new TextEncoder().encode(JSON.stringify(payload)).byteLength > 2 * 1024 * 1024) {
        throw new Error(
          t(
            'keys.import_too_large',
            'This batch exceeds the server request limit. Split it into smaller batches.',
          ),
        );
      }
      const response = await apiClient.post('/admin/keys/import', payload);
      return response.data;
    },
    onSuccess: () => {
      setImportModalVisible(false);
      setImportedKeys([]);
      setImportFileName('');
      importForm.resetFields();
      invalidateKeyQueries();
      message.success(t('keys.import_success', 'API keys imported successfully'));
    },
    onSettled: () => {
      importing.current = false;
    },
    onError: (error: any) => {
      if (error?.errorFields || error?.configurationFeedbackHandled) return;
      reportConfigurationError(error);
    },
  });

  const statusMutation = useMutation({
    mutationFn: async ({ id, status }: { id: number; status: string }) => {
      return apiClient.patch(`/admin/keys/${id}/status`, { status });
    },
    onSuccess: () => {
      message.success(t('keys.status_updated'));
      invalidateKeyQueries();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      return apiClient.delete(`/admin/keys/${id}`);
    },
    onSuccess: () => {
      message.success(t('keys.delete_success'));
      invalidateKeyQueries();
    },
  });

  const batchDeleteMutation = useMutation({
    mutationFn: async (ids: number[]) => {
      const res = await apiClient.post('/admin/keys/batch-delete', { ids });
      return res.data;
    },
    onSuccess: () => {
      message.success(t('keys.batch_delete_success', 'Selected API keys deleted successfully'));
      setSelectedRowKeys([]);
      invalidateKeyQueries();
    },
    onError: (error: any) => {
      reportConfigurationError(error);
    },
  });

  const resetMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await apiClient.post(`/admin/keys/${id}/reset`);
      return res.data;
    },
    onSuccess: data => {
      const newKey = data?.data?.key || data?.key;
      if (newKey) {
        setGeneratedKey(newKey);
        setNewKeyModalVisible(true);
        message.success(t('keys.reset_success'));
      }
      invalidateKeyQueries();
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (values: APIKeyFormValues) => {
      const payload = {
        name: values.name,
        organization_id: Number(values.organization_id ?? editingKey?.organization_id),
        expires_at: values.expires_at || null,
        allowed_provider_ids: values.allowed_provider_ids || [],
        allowed_models: values.allowed_models || [],
        quota_amount: values.quota_amount ?? null,
        quota_tokens: values.quota_tokens ?? null,
        daily_quota_amount: values.daily_quota_amount ?? null,
        weekly_quota_amount: values.weekly_quota_amount ?? null,
        monthly_quota_amount: values.monthly_quota_amount ?? null,
        daily_quota_tokens: values.daily_quota_tokens ?? null,
        weekly_quota_tokens: values.weekly_quota_tokens ?? null,
        monthly_quota_tokens: values.monthly_quota_tokens ?? null,
        ip_allowlist: values.ip_allowlist || [],
        security_inspection_upstream_enabled: values.security_inspection_upstream_enabled ?? true,
        security_inspection_downstream_enabled:
          values.security_inspection_downstream_enabled ?? true,
      };
      return apiClient.put(`/admin/keys/${editingKey!.id}`, payload);
    },
    onSuccess: () => {
      setEditModalVisible(false);
      setEditingKey(null);
      invalidateKeyQueries();
      message.success(t('keys.edit_success', 'API key updated'));
    },
  });

  const handleOpenEdit = (record: APIKeyRecord) => {
    setEditingKey(record);
    editForm.setFieldsValue({
      name: record.name,
      expires_at: record.expires_at,
      allowed_provider_ids: record.allowed_provider_ids || [],
      allowed_models: record.allowed_models || [],
      organization_id: record.organization_id,
      security_inspection_upstream_enabled: record.security_inspection_upstream_enabled ?? true,
      security_inspection_downstream_enabled: record.security_inspection_downstream_enabled ?? true,
      quota_amount: record.quota_amount,
      quota_tokens: record.quota_tokens,
      daily_quota_amount: record.daily_quota_amount,
      weekly_quota_amount: record.weekly_quota_amount,
      monthly_quota_amount: record.monthly_quota_amount,
      daily_quota_tokens: record.daily_quota_tokens,
      weekly_quota_tokens: record.weekly_quota_tokens,
      monthly_quota_tokens: record.monthly_quota_tokens,
      ip_allowlist: record.ip_allowlist || [],
    });
    editForm.setFields(
      Object.keys(editForm.getFieldsValue(true)).map(name => ({
        name: name as keyof APIKeyFormValues,
        touched: false,
      })),
    );
    setEditModalVisible(true);
  };

  const handleOpenCreate = (organizationID?: number) => {
    setSelectedTemplateID(undefined);
    setDuplicatingKey(null);
    form.resetFields();
    setKeyMode('generate');
    form.setFieldsValue({
      organization_id: currentUser?.role === 'super_admin' ? organizationID : currentOrgId,
      allowed_provider_ids: [],
      allowed_models: [],
      ip_allowlist: [],
    });
    form.setFields(
      Object.keys(form.getFieldsValue(true)).map(name => ({
        name: name as keyof APIKeyFormValues,
        touched: false,
      })),
    );
    setCreateModalVisible(true);
  };

  useFirstKeyAction(['create-key'], !!currentUser, (_, organizationID) => {
    handleOpenCreate(organizationID);
  });

  const applyTemplate = (id?: number) => {
    const template = keyTemplates.find(item => item.id === id);
    setSelectedTemplateID(id);
    form.setFieldsValue(
      template ? getKeyTemplateSettings(template.settings) : emptyKeyTemplateSettings(currentOrgId),
    );
  };

  const handleImportFile = async (file: File) => {
    if (importParsing || importing.current) return false;
    setImportParsing(true);
    try {
      const parsed = await parseAPIKeyImportFile(file);
      setImportedKeys(
        parsed.map((key, index) => ({
          ...key,
          name: key.name || `Imported API Key ${index + 1}`,
        })),
      );
      setImportFileName(file.name);
      importForm.resetFields();
      importForm.setFieldsValue(emptyKeyTemplateSettings(currentOrgId));
      setSelectedImportTemplateID(undefined);
      setImportGuideVisible(false);
      setImportModalVisible(true);
    } catch (error: any) {
      reportConfigurationError('field_validation');
    } finally {
      setImportParsing(false);
    }
    return false;
  };

  const columns: ColumnsType<APIKeyRecord> = [
    {
      title: t('keys.col_name', 'Key Name'),
      dataIndex: 'name',
      key: 'name',
      width: 130,
      render: (val: string, record: APIKeyRecord) => (
        <Tooltip title={val}>
          <Button
            type="link"
            className="console-record-title"
            style={{
              padding: 0,
              fontWeight: 600,
              maxWidth: 115,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              display: 'inline-block',
              textAlign: 'left',
              verticalAlign: 'middle',
            }}
            onClick={() => navigate(`/admin/keys/${record.id}`)}
          >
            {val}
          </Button>
        </Tooltip>
      ),
    },
    {
      title: t('keys.col_org', 'Organization'),
      key: 'organization',
      width: 140,
      ellipsis: true,
      render: (_: unknown, record: APIKeyRecord) => {
        const orgId = record.organization_id ?? record.org_id;
        const org = orgsData?.find(o => o.id === orgId);
        return <ResourceName kind="organizations" id={orgId} name={org?.name} />;
      },
    },
    {
      title: t('keys.col_status', 'Status'),
      dataIndex: 'status',
      key: 'status',
      width: 75,
      render: (status: string) => <StatusTag value={status} />,
    },
    {
      title: t('keys.col_key', 'API Key'),
      key: 'masked_key',
      width: 125,
      render: (_: string, record: APIKeyRecord) => (
        <KeySecretActions
          keyInfo={{ id: record.id, name: record.name, maskedKey: compactKey(record) }}
          compact
          showImport={false}
        />
      ),
    },
    {
      title: t('keys.usage_column'),
      key: 'quota',
      width: 340,
      render: (_: unknown, record: APIKeyRecord) => <KeyUsageCell record={record} />,
    },
    {
      title: t('keys.col_ip_allowlist', 'IP Allowlist'),
      dataIndex: 'ip_allowlist',
      key: 'ip_allowlist',
      width: 90,
      ellipsis: true,
      render: (list: string[]) =>
        list && list.length > 0 ? (
          list.join(', ')
        ) : (
          <Text type="secondary">{t('keys.any_ip', 'Any IP')}</Text>
        ),
    },
    {
      title: t('keys.col_actions', 'Actions'),
      key: 'actions',
      width: 200,
      fixed: isWideScreen ? ('right' as const) : undefined,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: unknown, record: APIKeyRecord) => (
        <div className="console-table-actions">
          <TableAction
            label={t('keys.cc_switch_import')}
            icon={<ExportOutlined />}
            onClick={() => setImportTarget(record)}
          />
          <TableAction
            label={t('keys.vary_ai_import')}
            icon={<ExportOutlined />}
            onClick={() => setVaryImportTarget(record)}
          />
          <TableAction
            label={t('keys.btn_edit')}
            icon={<EditOutlined />}
            onClick={() => handleOpenEdit(record)}
          />
          <TableAction
            label={t('keys.btn_duplicate')}
            icon={<CopyOutlined />}
            disabled={loadDuplicateMutation.isPending}
            loading={
              loadDuplicateMutation.isPending && loadDuplicateMutation.variables === record.id
            }
            onClick={() => loadDuplicateMutation.mutate(record.id)}
          />
          <TableActionMenu
            menu={{
              items: [
                {
                  key: 'status',
                  label: t(record.status === 'active' ? 'keys.btn_disable' : 'keys.btn_enable'),
                  icon: record.status === 'active' ? <StopOutlined /> : <CheckCircleOutlined />,
                  danger: record.status === 'active',
                  disabled: statusMutation.isPending,
                  onClick: () => {
                    const label = t(
                      record.status === 'active' ? 'keys.btn_disable' : 'keys.btn_enable',
                    );
                    Modal.confirm({
                      title: `${label} · ${record.name}`,
                      content: t(
                        record.status === 'active' ? 'ux.disable_key_note' : 'keys.btn_enable',
                      ),
                      okText: label,
                      cancelText: t('common.cancel'),
                      okButtonProps: { danger: record.status === 'active' },
                      onOk: () =>
                        statusMutation.mutateAsync({
                          id: record.id,
                          status: record.status === 'active' ? 'disabled' : 'active',
                        }),
                    });
                  },
                },
                {
                  key: 'delete',
                  label: t('common.delete'),
                  icon: <DeleteOutlined />,
                  danger: true,
                  disabled: deleteMutation.isPending,
                  onClick: () =>
                    Modal.confirm({
                      title: `${t('common.delete')} · ${record.name}`,
                      content: t('keys.confirm_delete_title'),
                      okText: t('common.delete'),
                      cancelText: t('common.cancel'),
                      okButtonProps: { danger: true },
                      onOk: () => deleteMutation.mutateAsync(record.id),
                    }),
                },
              ],
            }}
          />
        </div>
      ),
    },
  ];

  return (
    <Card>
      <ConsoleToolbar
        actions={
          <>
            {selectedRowKeys.length > 0 && (
              <Space size={8}>
                <Button onClick={() => setSelectedRowKeys([])}>
                  {t('keys.btn_clear_selection', 'Clear Selection')}
                </Button>
                <Popconfirm
                  title={t(
                    'keys.confirm_batch_delete_title',
                    'Delete the selected {count} API keys?',
                  ).replace('{count}', String(selectedRowKeys.length))}
                  description={t(
                    'keys.confirm_batch_delete_desc',
                    'Selected keys will be deactivated and deleted immediately. This action cannot be undone!',
                  )}
                  onConfirm={() => batchDeleteMutation.mutate(selectedRowKeys.map(Number))}
                  okButtonProps={{ danger: true, loading: batchDeleteMutation.isPending }}
                >
                  <Button danger icon={<DeleteOutlined />}>
                    {t('keys.btn_batch_delete', 'Batch Delete')} ({selectedRowKeys.length})
                  </Button>
                </Popconfirm>
              </Space>
            )}
            <Space size={8}>
              <Button type="primary" icon={<PlusOutlined />} onClick={() => handleOpenCreate()}>
                {t('keys.btn_new_key', 'New API Key')}
              </Button>
              <Button icon={<UploadOutlined />} onClick={() => setImportGuideVisible(true)}>
                {t('keys.btn_import_keys', 'Import API Keys')}
              </Button>
              <Button icon={<CopyOutlined />} onClick={() => setTemplatesVisible(true)}>
                {t('keys.templates_title')}
              </Button>
            </Space>
          </>
        }
      >
        <Select
          allowClear
          placeholder={t('keys.filter_org_placeholder', 'Filter by Organization')}
          aria-label={t('keys.filter_org_placeholder', 'Filter by Organization')}
          style={{ width: 170 }}
          value={filterOrg}
          onChange={v => {
            setFilterOrg(v);
            setPage(1);
            setSearchParams(prev => {
              const next = new URLSearchParams(prev);
              if (v) next.set('organization_id', String(v));
              else next.delete('organization_id');
              return next;
            });
          }}
          options={orgsData?.map(o => ({ label: o.name, value: o.id }))}
        />
        <SearchInput
          placeholder={t('keys.search_placeholder', 'Search key name, ID, or key value...')}
          allowClear
          value={search}
          onChange={e => {
            setSearch(e.target.value);
            if (!e.target.value) {
              setPage(1);
              setSearchParams(prev => {
                const next = new URLSearchParams(prev);
                next.delete('search');
                return next;
              });
            }
          }}
          onSearch={v => {
            setSearch(v);
            setPage(1);
            setSearchParams(prev => {
              const next = new URLSearchParams(prev);
              if (v) next.set('search', v);
              else next.delete('search');
              return next;
            });
          }}
          style={{ width: 230 }}
        />
      </ConsoleToolbar>
      <Table
        rowKey="id"
        rowSelection={{
          selectedRowKeys,
          onChange: newSelectedRowKeys => setSelectedRowKeys(newSelectedRowKeys),
        }}
        dataSource={keysData?.items || []}
        columns={columns}
        loading={isLoading}
        scroll={{ x: 1150 }}
        pagination={{
          current: page,
          pageSize,
          total: keysData?.total || 0,
          onChange: (p, ps) => {
            setPage(p);
            setPageSize(ps);
            setSelectedRowKeys([]);
          },
        }}
      />

      <Modal
        title={t('keys.import_title', 'Review API Keys Before Import')}
        open={importModalVisible}
        maskClosable={false}
        closable={!importMutation.isPending}
        keyboard={!importMutation.isPending}
        onCancel={() => {
          if (importing.current) return;
          setImportModalVisible(false);
          setImportedKeys([]);
          setImportFileName('');
          importForm.resetFields();
        }}
        onOk={() => {
          if (!importing.current) {
            importing.current = true;
            importMutation.mutate();
          }
        }}
        confirmLoading={importMutation.isPending}
        okText={t('keys.btn_confirm_import', 'Confirm and Import')}
        cancelText={t('common.cancel', 'Cancel')}
        cancelButtonProps={{ disabled: importMutation.isPending }}
        okButtonProps={{ disabled: importedKeys.length === 0 }}
        width={960}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t(
            'keys.import_review_notice',
            'Review every key value and name before confirming. Key values are shown only in this preview and will not be returned by the server.',
          )}
          description={
            importFileName ? `${importFileName} · ${importedKeys.length} keys` : undefined
          }
        />
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          name="key-import"
          form={importForm}
          layout="vertical"
          disabled={importMutation.isPending}
          style={{ marginBottom: 16 }}
        >
          <Alert
            type="info"
            showIcon
            message={t('keys.import_shared_settings')}
            style={{ marginBottom: 16 }}
          />
          {templatesError && <LoadError retry={refetchTemplates} />}
          <Form.Item label={t('keys.template_select')} extra={t('keys.template_select_help')}>
            <Select
              aria-label={t('keys.template_select')}
              allowClear
              showSearch
              optionFilterProp="label"
              loading={templatesLoading}
              value={selectedImportTemplateID}
              placeholder={t('keys.template_select_placeholder')}
              options={keyTemplates.map(item => ({ label: item.name, value: item.id }))}
              onChange={id => {
                setSelectedImportTemplateID(id);
                const template = keyTemplates.find(item => item.id === id);
                importForm.setFieldsValue(
                  template
                    ? getKeyTemplateSettings(template.settings)
                    : emptyKeyTemplateSettings(currentOrgId),
                );
              }}
            />
          </Form.Item>
          <KeyConfigurationFields
            key={selectedImportTemplateID || 'import'}
            enabled={importModalVisible}
            currentUser={currentUser}
            advancedOpen={!!selectedImportTemplateID}
          />
        </Form>
        <Table
          size="small"
          rowKey={(_, index) => String(index)}
          dataSource={importedKeys}
          pagination={false}
          scroll={{ y: 360, x: 800 }}
          columns={[
            {
              title: t('keys.import_col_name', 'Key Name'),
              dataIndex: 'name',
              width: 220,
              render: (name: string, _record: ImportedAPIKey, index: number) => (
                <Input
                  value={name}
                  disabled={importMutation.isPending}
                  maxLength={100}
                  onChange={event =>
                    setImportedKeys(current =>
                      current.map((key, keyIndex) =>
                        keyIndex === index ? { ...key, name: event.target.value } : key,
                      ),
                    )
                  }
                />
              ),
            },
            {
              title: t('keys.import_col_value', 'API Key Value'),
              dataIndex: 'api_key',
              render: (value: string) => (
                <code style={{ whiteSpace: 'normal', wordBreak: 'break-all' }}>{value}</code>
              ),
            },
          ]}
        />
      </Modal>

      {isError && <LoadError retry={refetch} />}
      {/* Create Key Modal */}
      <Modal
        title={
          duplicatingKey
            ? t('keys.btn_duplicate')
            : t('keys.modal_create_title', 'Create Organization API Key')
        }
        open={createModalVisible}
        maskClosable={false}
        onOk={() => form.submit()}
        onCancel={() => {
          if (!createMutation.isPending) cancelCreate();
        }}
        confirmLoading={createMutation.isPending}
        okText={t('keys.btn_submit_create', 'Create Key')}
        cancelText={t('common.cancel', 'Cancel')}
        width={640}
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
          disabled={createMutation.isPending}
          onFinish={values => {
            if (!creating.current) {
              creating.current = true;
              createMutation.mutate(values);
            }
          }}
        >
          <Form.Item hidden={!!duplicatingKey} label={t('keys.field_key_source', 'Key Source')}>
            <Select
              value={keyMode}
              onChange={(value: 'generate' | 'existing') => {
                setKeyMode(value);
                form.setFieldValue('api_key', undefined);
              }}
              options={[
                { label: t('keys.opt_generate_new', 'Generate a new API key'), value: 'generate' },
                { label: t('keys.opt_use_existing', 'Use an existing API key'), value: 'existing' },
              ]}
            />
          </Form.Item>
          {!duplicatingKey && (
            <>
              {templatesError && <LoadError retry={refetchTemplates} />}
              <Form.Item label={t('keys.template_select')} extra={t('keys.template_select_help')}>
                <Select
                  aria-label={t('keys.template_select')}
                  allowClear
                  showSearch
                  optionFilterProp="label"
                  loading={templatesLoading}
                  value={selectedTemplateID}
                  placeholder={t('keys.template_select_placeholder')}
                  options={keyTemplates.map(item => ({ label: item.name, value: item.id }))}
                  onChange={applyTemplate}
                />
              </Form.Item>
            </>
          )}
          {keyMode === 'existing' && (
            <Form.Item
              name="api_key"
              label={t('keys.field_existing_key', 'Existing API Key')}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: t('keys.enter_existing_key', 'Please enter the existing API key'),
                },
              ]}
            >
              <Input.Password
                autoComplete="new-password"
                placeholder={t('keys.paste_existing_key', 'Paste the API key to register')}
              />
            </Form.Item>
          )}
          <Form.Item
            name="name"
            label={t('keys.field_name')}
            rules={[{ required: true, whitespace: true, message: t('keys.enter_key_name') }]}
          >
            <Input maxLength={100} placeholder={t('keys.key_name_placeholder')} />
          </Form.Item>

          <KeyConfigurationFields
            key={duplicatingKey?.id || selectedTemplateID || 'new'}
            enabled={createModalVisible}
            currentUser={currentUser}
            advancedOpen={!!duplicatingKey || !!selectedTemplateID}
            showStatus={!!duplicatingKey}
            showRollingLimits={false}
            showIPBlocklist={false}
          />
        </Form>
      </Modal>

      <Modal
        title={t('keys.modal_edit_title', 'Edit API Key')}
        open={editModalVisible}
        footer={(_, { OkBtn, CancelBtn }) => (
          <div style={{ display: 'flex', justifyContent: 'space-between' }}>
            <Button
              type="text"
              danger
              disabled={updateMutation.isPending || resetMutation.isPending}
              onClick={() => {
                if (!editingKey) return;
                Modal.confirm({
                  title: `${t('keys.btn_reset')} · ${editingKey.name}`,
                  content: t('keys.confirm_reset_desc'),
                  okText: t('keys.btn_reset'),
                  cancelText: t('common.cancel'),
                  okButtonProps: { danger: true },
                  onOk: () => resetMutation.mutateAsync(editingKey.id),
                });
              }}
            >
              {t('keys.btn_reset')}
            </Button>
            <Space>
              <CancelBtn />
              <OkBtn />
            </Space>
          </div>
        )}
        onOk={() => editForm.submit()}
        onCancel={() => {
          if (!updateMutation.isPending) cancelEdit();
        }}
        confirmLoading={updateMutation.isPending}
        okText={t('common.save', 'Save')}
        cancelText={t('common.cancel', 'Cancel')}
        width={640}
      >
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          form={editForm}
          layout="vertical"
          disabled={updateMutation.isPending}
          onFinish={values => {
            if (!updateMutation.isPending) updateMutation.mutate(values);
          }}
        >
          <Form.Item
            name="name"
            label={t('keys.field_name', 'Key Name')}
            rules={[
              {
                required: true,
                whitespace: true,
                message: t('keys.enter_key_name', 'Please enter key name'),
              },
            ]}
          >
            <Input maxLength={100} />
          </Form.Item>
          {currentUser?.role === 'super_admin' ? (
            <Form.Item
              name="organization_id"
              label={t('keys.field_org')}
              rules={[{ required: true }]}
            >
              <Select
                showSearch
                optionFilterProp="label"
                options={(orgsData || [])
                  .filter((org: any) => org.status === 'active')
                  .map((org: any) => ({ value: org.id, label: org.name }))}
              />
            </Form.Item>
          ) : (
            <Form.Item label={t('keys.field_org')}>
              <ResourceName kind="organizations" id={editingKey?.organization_id} />
            </Form.Item>
          )}
          <Form.Item
            name="security_inspection_upstream_enabled"
            label={t('keys.security_inspection_upstream', 'Upstream security inspection')}
            valuePropName="checked"
            extra={t(
              'keys.security_inspection_upstream_help',
              'Check requests before they are sent to the model provider.',
            )}
          >
            <Switch />
          </Form.Item>
          <Form.Item
            name="security_inspection_downstream_enabled"
            label={t('keys.security_inspection_downstream', 'Downstream security inspection')}
            valuePropName="checked"
            extra={t(
              'keys.security_inspection_downstream_help',
              'Check model responses before they are returned to the client.',
            )}
          >
            <Switch />
          </Form.Item>
          <AdvancedFields>
            <KeyAccessFields
              enabled={editModalVisible}
              organizationID={Number(editingOrganizationID || editingKey?.organization_id)}
            />
            <Form.Item
              name="expires_at"
              label={t('common.expires_at')}
              extra={t('keys.expiration_help')}
            >
              <KeyExpiryInput />
            </Form.Item>
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item
                  name="quota_tokens"
                  label={t('keys.field_quota_tokens', 'Token Quota (Optional)')}
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
                  name="quota_amount"
                  label={t('keys.field_quota_amount', 'Amount Quota USD (Optional)')}
                >
                  <InputNumber
                    min={0}
                    style={{ width: '100%' }}
                    placeholder={t('keys.quota_amount_placeholder', 'Unlimited')}
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
          </AdvancedFields>
        </Form>
      </Modal>

      {/* One-time Key Display Modal */}
      <Modal
        title={t('keys.modal_generated_title', 'API Key Generated')}
        open={newKeyModalVisible}
        maskClosable={false}
        onOk={() => {
          setNewKeyModalVisible(false);
          setGeneratedKey('');
        }}
        onCancel={() => {
          setNewKeyModalVisible(false);
          setGeneratedKey('');
        }}
        okText={t('keys.btn_i_have_copied', 'I Have Copied and Stored This Key')}
        footer={(_, { OkBtn }) => (
          <>
            <KeyCopyAllButton apiKey={generatedKey} />
            <OkBtn />
          </>
        )}
        width={600}
      >
        <Paragraph type="secondary">{t('keys.revealed_desc')}</Paragraph>

        <KeyConnectionInfo apiKey={generatedKey} />
      </Modal>

      <KeyImportGuide
        open={importGuideVisible}
        parsing={importParsing}
        onClose={() => setImportGuideVisible(false)}
        onFile={handleImportFile}
      />
      <KeyTemplatesModal
        open={templatesVisible}
        onClose={() => setTemplatesVisible(false)}
        currentUser={currentUser}
      />
      <KeyClientImportModal
        target="cc-switch"
        keyInfo={importTarget}
        onClose={() => setImportTarget(null)}
      />
      <KeyClientImportModal
        target="vary-ai"
        keyInfo={varyImportTarget}
        onClose={() => setVaryImportTarget(null)}
      />
    </Card>
  );
};
