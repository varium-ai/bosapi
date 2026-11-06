import { defaultPriceConfirmed, modelPriceSource, saveDefaultPricing } from './pricingState';
import { reportConfigurationError } from '../diagnostics/reportConfigurationError';
import { TableAction, TableActionMenu } from '../common/TableAction';
import { RecordTitle } from '../common/RecordTitle';
import { copyText } from '../common/copyText';
import { RecordDetails } from '../common/RecordDetails';
import React, { useRef, useState } from 'react';
import {
  Card,
  Table,
  Button,
  Space,
  Modal,
  Form,
  Input,
  Select,
  Tag,
  message,
  InputNumber,
  Drawer,
  Typography,
  Switch,
  Row,
  Col,
  Divider,
  Result,
  Tooltip,
  Alert,
  Popconfirm,
} from 'antd';
import {
  PlusOutlined,
  CompassOutlined,
  SyncOutlined,
  CopyOutlined,
  ClearOutlined,
  TableOutlined,
  CodeOutlined,
  QuestionCircleOutlined,
  DeleteOutlined,
} from '@ant-design/icons';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { ModelCapabilitiesTags } from './ModelCapabilitiesTags';
import { AdvancedFields, LoadError, PageHelp, useDiscardChanges } from '../common/ConsoleUX';
import { SegmentedControl } from '../common/SegmentedControl';
import { useFirstKeyAction } from '../onboarding/useFirstKeyAction';
import { invalidateFirstKeySetup } from '../onboarding/setupQueries';
import { SubscriptionAuthorization, SUBSCRIPTION_DEFAULTS } from './SubscriptionAuthorization';

const { Text } = Typography;

const PLATFORM_DEFAULTS: Record<string, { base_url: string; api_format: string }> = {
  openai: { base_url: 'https://api.openai.com/v1', api_format: 'openai_chat' },
  anthropic: { base_url: 'https://api.anthropic.com', api_format: 'anthropic_messages' },
  gemini: { base_url: 'https://generativelanguage.googleapis.com', api_format: 'gemini' },
  deepseek: { base_url: 'https://api.deepseek.com/v1', api_format: 'openai_chat' },
  grok: { base_url: 'https://api.x.ai/v1', api_format: 'openai_chat' },
  kimi: { base_url: 'https://api.moonshot.cn/v1', api_format: 'openai_chat' },
  zhipu: { base_url: 'https://open.bigmodel.cn/api/paas/v4', api_format: 'openai_chat' },
  minimax: { base_url: 'https://api.minimaxi.com/v1', api_format: 'openai_chat' },
  antigravity: { base_url: '', api_format: 'openai_chat' },
  composite: { base_url: '', api_format: 'openai_chat' },
};

const PLATFORM_PRESETS: Record<string, { relevant: string[]; all: string[] }> = {
  openai: {
    relevant: [
      'gpt-4o',
      'gpt-4o-mini',
      'o1',
      'o3-mini',
      'gpt-4-turbo',
      'text-embedding-3-small',
      'text-embedding-3-large',
    ],
    all: [
      'gpt-4o',
      'gpt-4o-mini',
      'o1',
      'o1-mini',
      'o1-preview',
      'o3-mini',
      'gpt-4-turbo',
      'gpt-4',
      'gpt-3.5-turbo',
      'text-embedding-3-small',
      'text-embedding-3-large',
      'text-embedding-ada-002',
      'dall-e-3',
    ],
  },
  anthropic: {
    relevant: [
      'claude-3-7-sonnet-20250219',
      'claude-3-5-sonnet-20241022',
      'claude-3-5-haiku-20241022',
    ],
    all: [
      'claude-3-7-sonnet-20250219',
      'claude-3-5-sonnet-20241022',
      'claude-3-5-haiku-20241022',
      'claude-3-opus-20240229',
      'claude-3-sonnet-20240229',
      'claude-3-haiku-20240307',
    ],
  },
  gemini: {
    relevant: [
      'gemini-2.5-pro',
      'gemini-2.5-flash',
      'gemini-2.0-flash',
      'gemini-1.5-pro',
      'gemini-1.5-flash',
    ],
    all: [
      'gemini-2.5-pro',
      'gemini-2.5-flash',
      'gemini-2.0-flash',
      'gemini-2.0-flash-lite',
      'gemini-1.5-pro',
      'gemini-1.5-flash',
      'gemini-1.5-flash-8b',
      'text-embedding-004',
    ],
  },
  deepseek: {
    relevant: ['deepseek-chat', 'deepseek-reasoner'],
    all: ['deepseek-chat', 'deepseek-reasoner', 'deepseek-coder'],
  },
  zhipu: {
    relevant: ['glm-4-plus', 'glm-4-air', 'glm-4-flash', 'glm-4-long', 'glm-zero-preview'],
    all: [
      'glm-4-plus',
      'glm-4-air',
      'glm-4-airx',
      'glm-4-flash',
      'glm-4-flashx',
      'glm-4-long',
      'glm-4v-plus',
      'glm-zero-preview',
      'cogview-3-plus',
      'embedding-3',
    ],
  },
  kimi: {
    relevant: ['moonshot-v1-8k', 'moonshot-v1-32k', 'moonshot-v1-128k'],
    all: ['moonshot-v1-8k', 'moonshot-v1-32k', 'moonshot-v1-128k', 'moonshot-v1-auto'],
  },
  minimax: {
    relevant: ['abab6.5s-chat', 'abab6.5t-chat', 'abab6.5g-chat'],
    all: ['abab6.5s-chat', 'abab6.5t-chat', 'abab6.5g-chat', 'embo-01'],
  },
  grok: {
    relevant: ['grok-3', 'grok-3-mini', 'grok-2', 'grok-2-mini'],
    all: ['grok-3', 'grok-3-mini', 'grok-2', 'grok-2-mini', 'grok-2-vision'],
  },
  antigravity: {
    relevant: ['gemini-2.5-pro', 'gemini-2.5-flash', 'claude-3-7-sonnet'],
    all: ['gemini-2.5-pro', 'gemini-2.5-flash', 'claude-3-7-sonnet', 'gpt-4o'],
  },
  composite: {
    relevant: ['composite/auto'],
    all: ['composite/auto', 'composite/fast', 'composite/quality'],
  },
};

interface Provider {
  id: number;
  name: string;
  platform: string;
  account_type: string;
  api_format: string;
  base_url: string;
  status: 'active' | 'disabled' | 'rate_limited';
  priority: number;
  concurrency_limit?: number;
  schedulable?: boolean;
  rate_limit_rpm?: number;
  capabilities?: Record<string, any>;
  pricing?: {
    input?: number;
    output?: number;
    confirmed?: boolean;
    cache_read?: number;
    cache_write?: number;
    models?: Record<
      string,
      { input?: number; output?: number; cache_read?: number; cache_write?: number }
    >;
  };
  model_mapping?: Record<string, string>;
  created_by_user_id?: number;
  verified_at?: string | null;
  updated_at?: string;
  created_at?: string;
}

interface DiscoveredModel {
  id: number;
  provider_id: number;
  model_name: string;
  capabilities?: Record<string, any>;
  price_input_per_1m: number;
  price_output_per_1m: number;
  price_cache_read_per_1m?: number;
  price_cache_write_per_1m?: number;
  status: string;
  discovered_at?: string;
}

interface MappingModelSelectProps {
  value?: string;
  onChange: (val: string) => void;
  options: Array<{ label: string; value: string }>;
  placeholder?: string;
  customPlaceholder?: string;
}

const MappingModelSelect: React.FC<MappingModelSelectProps> = ({
  value,
  onChange,
  options,
  placeholder,
  customPlaceholder,
}) => {
  const { t } = useI18n();
  const [search, setSearch] = useState('');

  const displayOptions = React.useMemo(() => {
    let list = options;
    if (value && !options.some(o => o.value === value)) {
      list = [{ label: value, value }, ...list];
    }
    const trimmed = search.trim();
    if (!trimmed) return list;
    const exists = list.some(o => o.value.toLowerCase() === trimmed.toLowerCase());
    if (!exists) {
      return [
        {
          label: t('providers.use_custom_model', '+ Use "{model}"').replace('{model}', trimmed),
          value: trimmed,
        },
        ...list,
      ];
    }
    return list;
  }, [search, options, value, t]);

  return (
    <Select
      showSearch
      allowClear
      options={displayOptions}
      placeholder={placeholder}
      value={value || undefined}
      searchValue={search}
      onSearch={setSearch}
      onOpenChange={open => {
        if (!open) setSearch('');
      }}
      onChange={val => {
        onChange(val || '');
        setSearch('');
      }}
      filterOption={(input, option) =>
        (option?.label ?? '').toLowerCase().includes(input.toLowerCase())
      }
      popupRender={menu => (
        <>
          {menu}
          <Divider style={{ margin: '6px 0' }} />
          <div style={{ padding: '0 8px 6px' }}>
            <Input
              size="small"
              placeholder={customPlaceholder}
              onKeyDown={e => e.stopPropagation()}
              onPressEnter={e => {
                const val = (e.target as HTMLInputElement).value.trim();
                if (val) {
                  onChange(val);
                  setSearch('');
                  (e.target as HTMLInputElement).value = '';
                }
              }}
            />
          </div>
        </>
      )}
      style={{ width: '100%', borderRadius: 6 }}
    />
  );
};

export const ProvidersPage: React.FC = () => {
  const { t, formatCurrency, formatDateTime, timezoneBadge } = useI18n();
  const queryClient = useQueryClient();
  const [modalVisible, setModalVisible] = useState(false);
  const [modelsDrawerVisible, setModelsDrawerVisible] = useState(false);
  const [selectedProvider, setSelectedProvider] = useState<Provider | null>(null);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [editingModel, setEditingModel] = useState<DiscoveredModel | null>(null);
  const savingModel = useRef(false);
  const [modelPriceModalVisible, setModelPriceModalVisible] = useState(false);
  const [testingId, setTestingId] = useState<number | null>(null);
  const [discoveringId, setDiscoveringId] = useState<number | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [platformFilter, setPlatformFilter] = useState<string | undefined>();
  const [isSyncingKb, setIsSyncingKb] = useState(false);

  const [form] = Form.useForm();
  const [detail, setDetail] = useState<Provider | null>(null);
  const cancel = useDiscardChanges(form, () => setModalVisible(false));
  const [modelPriceForm] = Form.useForm();
  const currentUser = React.useMemo(() => {
    try {
      const raw = localStorage.getItem('user_info');
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  }, []);
  const isSuperAdmin = currentUser?.role === 'super_admin';
  const canModifyProvider = (provider: Provider | null) => !!provider && isSuperAdmin;

  const handleSyncKnowledgeBase = async () => {
    setIsSyncingKb(true);
    try {
      const res = await apiClient.post('/admin/model-metadata/sync?source=openrouter');
      const count = res.data?.data?.count || 0;
      const rawSource = res.data?.data?.source || 'OpenRouter';
      const source = rawSource === 'openrouter' ? 'OpenRouter' : rawSource;
      message.success(
        t(
          'providers.sync_kb_success',
          'Successfully synced {count} official model specs from {source}!',
        )
          .replace('{source}', source)
          .replace('{count}', String(count)),
      );
      if (selectedProvider) {
        queryClient.invalidateQueries({ queryKey: ['provider-models', selectedProvider.id] });
        await discoverModels(selectedProvider.id, true);
      }
    } catch (err: any) {
      reportConfigurationError(error);
    } finally {
      setIsSyncingKb(false);
    }
  };

  const {
    data: providersData,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['providers', page, pageSize, platformFilter],
    queryFn: async () => {
      const res = await apiClient.get('/admin/providers', {
        params: {
          page,
          page_size: pageSize,
          platform: platformFilter || undefined,
        },
      });
      return res.data?.data || { items: [], total: 0 };
    },
    retry: false,
  });

  const {
    data: discoveredModels,
    isLoading: isLoadingModels,
    isError: modelsError,
    refetch: refetchModels,
  } = useQuery({
    queryKey: ['provider-models', selectedProvider?.id],
    queryFn: async () => {
      if (!selectedProvider) return [];
      const res = await apiClient.get(`/admin/providers/${selectedProvider.id}/models`, {
        params: { page: 1, page_size: 100 },
      });
      return (res.data?.data?.items || []) as DiscoveredModel[];
    },
    enabled: !!selectedProvider && modelsDrawerVisible,
  });

  const saveMutation = useMutation({
    mutationFn: async (values: any) => {
      let modelMapping: Record<string, string> = {};
      if (mappingMode === 'visual') {
        mappingRows.forEach(r => {
          const k = r.from.trim();
          const v = r.to.trim();
          if (k && v) {
            modelMapping[k] = v;
          }
        });
      } else if (mappingJsonText && mappingJsonText.trim()) {
        try {
          modelMapping = JSON.parse(mappingJsonText);
          if (!modelMapping || typeof modelMapping !== 'object' || Array.isArray(modelMapping)) {
            throw new Error('invalid format');
          }
        } catch {
          form.setFields([
            { name: 'model_mapping', errors: [t('providers.model_mapping_invalid_json')] },
          ]);
          throw new Error('invalid_model_mapping');
        }
      }
      const payload = {
        name: values.name,
        platform: values.platform,
        account_type: values.account_type,
        api_format: values.api_format,
        base_url: values.base_url,
        api_key: values.api_key || '',
        priority: values.priority || 0,
        concurrency_limit: values.concurrency_limit ?? null,
        schedulable: values.schedulable ?? true,
        rate_limit_rpm: values.rate_limit_rpm ?? null,
        capabilities: editingProvider?.capabilities || {},
        pricing: saveDefaultPricing(
          editingProvider?.pricing,
          values.price_input,
          values.price_output,
          values.price_cache_read,
          values.price_cache_write,
        ),
        model_mapping: modelMapping,
        models: values.models || [],
      };
      if (values.account_type === 'oauth' && values.oauth_authorization?.session_id) {
        const authorization = values.oauth_authorization;
        return apiClient.post(`/admin/providers/oauth/${values.platform}/complete`, {
          provider_id: editingProvider?.id || 0,
          session_id: authorization.session_id,
          callback: authorization.callback.trim(),
          state: authorization.state,
          project_id: authorization.project_id?.trim() || '',
          provider: payload,
        });
      }
      if (editingProvider) {
        return apiClient.put(`/admin/providers/${editingProvider.id}`, payload);
      }
      return apiClient.post('/admin/providers', payload);
    },
    onSuccess: async (response, values) => {
      message.success(t('providers.saved_success', 'Provider saved successfully'));
      setModalVisible(false);
      form.resetFields();
      queryClient.invalidateQueries({ queryKey: ['providers'] });
      invalidateFirstKeySetup(queryClient);
      if (editingProvider) {
        queryClient.invalidateQueries({ queryKey: ['provider-models', editingProvider.id] });
      }
      const saved = response.data?.data as Provider | undefined;
      if ((values.discover_models || (!editingProvider && !values.models?.length)) && saved?.id) {
        setSelectedProvider(saved);
        setModelsDrawerVisible(true);
        const items = await discoverModels(saved.id);
        if (items === null) message.warning(t('qa.saved_discovery_failed'));
        else if (!items.length) {
          message.warning(
            t(
              'providers.subscription_models_pending',
              'The channel is saved, but no models are configured. Retry model discovery or choose Edit Provider to add models manually.',
            ),
          );
        }
      }
    },
  });

  const updateModelMutation = useMutation({
    mutationFn: async (values: any) => {
      if (!selectedProvider || !editingModel) return;
      const caps = { ...(editingModel.capabilities || {}) };
      if (values.supports_vision !== undefined) caps.vision = Boolean(values.supports_vision);
      if (values.supports_video !== undefined) caps.video = Boolean(values.supports_video);
      if (values.supports_tools !== undefined) caps.tools = Boolean(values.supports_tools);
      if (values.supports_reasoning !== undefined)
        caps.reasoning = Boolean(values.supports_reasoning);
      if (values.max_input_tokens !== undefined)
        caps.max_input_tokens = Number(values.max_input_tokens);

      return apiClient.put(`/admin/providers/${selectedProvider.id}/models/${editingModel.id}`, {
        price_input_per_1m: Number(values.price_input_per_1m || 0),
        price_output_per_1m: Number(values.price_output_per_1m || 0),
        price_cache_read_per_1m: Number(values.price_cache_read_per_1m || 0),
        price_cache_write_per_1m: Number(values.price_cache_write_per_1m || 0),
        status: values.status || 'active',
        capabilities: caps,
      });
    },
    onSuccess: (_, values) => {
      if (selectedProvider && editingModel)
        setSelectedProvider({
          ...selectedProvider,
          pricing: {
            ...selectedProvider.pricing,
            models: {
              ...selectedProvider.pricing?.models,
              [editingModel.model_name]: {
                input: Number(values.price_input_per_1m || 0),
                output: Number(values.price_output_per_1m || 0),
                cache_read: Number(values.price_cache_read_per_1m || 0),
                cache_write: Number(values.price_cache_write_per_1m || 0),
              },
            },
          },
        });
      message.success(t('providers.model_pricing_updated', 'Model pricing updated successfully'));
      setModelPriceModalVisible(false);
      setEditingModel(null);
      modelPriceForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['provider-models', selectedProvider?.id] });
      queryClient.invalidateQueries({ queryKey: ['providers'] });
      invalidateFirstKeySetup(queryClient);
    },
    onSettled: () => {
      savingModel.current = false;
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      return apiClient.delete(`/admin/providers/${id}`);
    },
    onSuccess: () => {
      message.success(t('providers.provider_deleted', 'Provider permanently deleted'));
      queryClient.invalidateQueries({ queryKey: ['providers'] });
      invalidateFirstKeySetup(queryClient);
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, status }: { id: number; status: 'active' | 'disabled' }) => {
      return apiClient.patch(`/admin/providers/${id}/status`, { status });
    },
    onSuccess: (_, vars) => {
      if (vars.status === 'active') {
        message.success(t('providers.provider_enabled', 'Provider enabled successfully'));
      } else {
        message.success(t('providers.provider_disabled', 'Provider disabled successfully'));
      }
      queryClient.invalidateQueries({ queryKey: ['providers'] });
      invalidateFirstKeySetup(queryClient);
    },
  });

  const testConnectivity = async (id: number) => {
    setTestingId(id);
    try {
      const res = await apiClient.post(`/admin/providers/${id}/test`);
      const data = res.data?.data || res.data;
      if (data?.success) {
        invalidateFirstKeySetup(queryClient);
        void queryClient.invalidateQueries({ queryKey: ['providers'] });
        message.success(
          t('providers.test_success', 'Connectivity test passed! Latency: {latency}ms').replace(
            '{latency}',
            String(data.latency_ms ?? 0),
          ),
        );
      } else {
        reportConfigurationError('unknown_error');
      }
    } catch {
      // Handled by axios
    } finally {
      setTestingId(null);
    }
  };

  const discoverModels = async (id: number, silent = false) => {
    setDiscoveringId(id);
    try {
      const res = await apiClient.post(`/admin/providers/${id}/discover-models`);
      const items = res.data?.data?.items || [];
      if (!silent) {
        message.success(
          t('providers.discover_success', 'Successfully discovered {count} models!').replace(
            '{count}',
            String(items.length),
          ),
        );
      }
      queryClient.invalidateQueries({ queryKey: ['provider-models', id] });
      invalidateFirstKeySetup(queryClient);
      return items as DiscoveredModel[];
    } catch {
      // Handled
      return null;
    } finally {
      setDiscoveringId(null);
    }
  };

  const selectedPlatform = Form.useWatch('platform', form);
  const selectedAccountType = Form.useWatch('account_type', form);
  const isSubscription = selectedAccountType === 'oauth';
  const currentModels = (Form.useWatch('models', form) || []) as string[];

  const handlePlatformChange = (platform: string) => {
    const previous = PLATFORM_DEFAULTS[selectedPlatform];
    const next = PLATFORM_DEFAULTS[platform];
    if (!next) return;

    // Keep explicit overrides and saved channel settings when switching platforms.
    for (const field of ['base_url', 'api_format'] as const) {
      const current = form.getFieldValue(field);
      if (!current || (!editingProvider && current === previous?.[field])) {
        form.setFieldValue(field, next[field]);
      }
    }
  };

  const [mappingMode, setMappingMode] = useState<'visual' | 'json'>('visual');
  const [mappingRows, setMappingRows] = useState<Array<{ id: string; from: string; to: string }>>(
    [],
  );
  const [mappingJsonText, setMappingJsonText] = useState<string>('');

  const syncRowsToJson = (rows: Array<{ from: string; to: string }>) => {
    const obj: Record<string, string> = {};
    rows.forEach(r => {
      const k = r.from.trim();
      const v = r.to.trim();
      if (k && v) {
        obj[k] = v;
      }
    });
    const json = Object.keys(obj).length > 0 ? JSON.stringify(obj, null, 2) : '';
    setMappingJsonText(json);
    form.setFieldValue('model_mapping', json);
  };

  const handleUpdateRow = (id: string, field: 'from' | 'to', value: string) => {
    setMappingRows(prev => {
      const next = prev.map(r => (r.id === id ? { ...r, [field]: value } : r));
      syncRowsToJson(next);
      return next;
    });
  };

  const handleAddRow = () => {
    const defaultTo = upstreamDiscoveredModels[0] || currentModels[0] || '';
    const newRow = {
      id: `m-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      from: '',
      to: defaultTo,
    };
    setMappingRows(prev => {
      const next = [...prev, newRow];
      syncRowsToJson(next);
      return next;
    });
  };

  const handleRemoveRow = (id: string) => {
    setMappingRows(prev => {
      const next = prev.filter(r => r.id !== id);
      syncRowsToJson(next);
      return next;
    });
  };

  const handleJsonChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value;
    setMappingJsonText(val);
    form.setFieldValue('model_mapping', val);
  };

  const handleModeChange = (mode: 'visual' | 'json') => {
    if (mode === 'json') {
      syncRowsToJson(mappingRows);
      setMappingMode('json');
    } else {
      if (!mappingJsonText || !mappingJsonText.trim()) {
        setMappingRows([]);
        setMappingMode('visual');
        return;
      }
      try {
        const parsed = JSON.parse(mappingJsonText);
        if (
          parsed &&
          typeof parsed === 'object' &&
          !Array.isArray(parsed) &&
          Object.values(parsed).every(item => typeof item === 'string')
        ) {
          const rows = Object.entries(parsed).map(([from, to], i) => ({
            id: `m-${Date.now()}-${i}`,
            from,
            to: String(to),
          }));
          setMappingRows(rows);
          setMappingMode('visual');
        } else {
          form.setFields([
            { name: 'model_mapping', errors: [t('providers.model_mapping_invalid_json')] },
          ]);
        }
      } catch {
        form.setFields([
          { name: 'model_mapping', errors: [t('providers.model_mapping_invalid_json')] },
        ]);
      }
    }
  };

  const isIgnoredMappingModel = (m?: string) => {
    if (!m) return true;
    const lower = m.trim().toLowerCase();
    return (
      lower === 'codex-auto-review' ||
      lower === 'codex-auto-view' ||
      lower.startsWith('codex-auto-')
    );
  };

  const handleFillTemplate = () => {
    const validUpstream = (upstreamDiscoveredModels || []).find(m => !isIgnoredMappingModel(m));
    const validCurrent = (currentModels || []).find(m => !isIgnoredMappingModel(m));
    const target =
      validUpstream ||
      validCurrent ||
      (selectedPlatform === 'zhipu' ? 'zai-org/GLM-5.3-Flash' : 'gpt-4o');
    const clientName = target.includes('/')
      ? target.split('/')[1]
      : target.startsWith('codex-')
        ? 'codex'
        : selectedPlatform === 'zhipu'
          ? 'glm-4'
          : 'gpt-4';
    const sampleRows = [{ id: `tpl-${Date.now()}-1`, from: clientName, to: target }];
    setMappingRows(sampleRows);
    syncRowsToJson(sampleRows);
    message.success(t('providers.fill_template', '已填入模版'));
  };

  const handleMapAll = () => {
    const candidates = new Set<string>();
    (upstreamDiscoveredModels || []).forEach(m => {
      const trimmed = m?.trim();
      if (trimmed && !isIgnoredMappingModel(trimmed)) candidates.add(trimmed);
    });
    (currentModels || []).forEach(m => {
      const trimmed = m?.trim();
      if (trimmed && !isIgnoredMappingModel(trimmed)) candidates.add(trimmed);
    });

    if (candidates.size === 0) {
      message.info(t('providers.no_models_to_map', '暂无可映射的上游模型，请先拉取或添加模型'));
      return;
    }

    const newRows: Array<{ id: string; from: string; to: string }> = [];
    let idx = 1;
    candidates.forEach(target => {
      newRows.push({
        id: `map-all-${Date.now()}-${idx++}`,
        from: target,
        to: target,
      });
    });

    setMappingRows(newRows);
    syncRowsToJson(newRows);
    message.success(
      t('providers.map_all_success', '已为 {count} 个模型生成映射关系').replace(
        '{count}',
        String(newRows.length),
      ),
    );
  };

  const handleClearAllMappings = () => {
    setMappingRows([]);
    setMappingJsonText('');
    form.setFieldValue('model_mapping', '');
    message.success(t('providers.mappings_cleared', '已清空所有模型映射'));
  };

  const handleFormatMappingJson = () => {
    if (!mappingJsonText || !mappingJsonText.trim()) return;
    try {
      const obj = JSON.parse(mappingJsonText);
      if (
        !obj ||
        typeof obj !== 'object' ||
        Array.isArray(obj) ||
        !Object.values(obj).every(item => typeof item === 'string')
      )
        throw new Error('invalid_model_mapping');
      const formatted = JSON.stringify(obj, null, 2);
      setMappingJsonText(formatted);
      form.setFieldValue('model_mapping', formatted);
      message.success(t('common.success', 'Formatted successfully'));
    } catch {
      form.setFields([
        { name: 'model_mapping', errors: [t('providers.model_mapping_invalid_json')] },
      ]);
    }
  };

  const parsedMapping = React.useMemo(() => {
    if (mappingMode === 'visual') {
      const obj: Record<string, string> = {};
      mappingRows.forEach(r => {
        const k = r.from.trim();
        const v = r.to.trim();
        if (k && v) obj[k] = v;
      });
      return obj;
    }
    if (!mappingJsonText || typeof mappingJsonText !== 'string') return {};
    try {
      const obj = JSON.parse(mappingJsonText);
      if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
        return obj as Record<string, string>;
      }
    } catch {
      // Keep the empty preview while the mapping JSON is incomplete.
    }
    return {};
  }, [mappingMode, mappingRows, mappingJsonText]);

  const conflictingTargets = React.useMemo(() => {
    const targets = new Set<string>();
    Object.values(parsedMapping).forEach(val => {
      if (typeof val === 'string' && val.trim()) {
        targets.add(val.trim());
      }
    });
    return currentModels.filter(m => targets.has(m));
  }, [parsedMapping, currentModels]);

  const [upstreamDiscoveredModels, setUpstreamDiscoveredModels] = useState<string[]>([]);

  const { data: officialMetadata } = useQuery({
    queryKey: ['model-metadata-catalog'],
    queryFn: async () => {
      try {
        const res = await apiClient.get('/admin/model-metadata', {
          params: { page: 1, page_size: 100 },
        });
        return (res.data?.data?.items || []) as Array<{ model_id: string; display_name: string }>;
      } catch {
        return [];
      }
    },
    enabled: isSuperAdmin,
    staleTime: 60000,
  });

  const modelSelectOptions = React.useMemo(() => {
    const set = new Set<string>();

    // 1. Current selected/typed models
    (currentModels || []).forEach(m => {
      if (m && typeof m === 'string') set.add(m.trim());
    });

    // 2. Upstream models discovered for this channel
    (upstreamDiscoveredModels || []).forEach(m => {
      if (m && typeof m === 'string') set.add(m.trim());
    });

    // 3. Official models from knowledge base matching current platform (if no upstream models discovered yet)
    if (upstreamDiscoveredModels.length === 0 && officialMetadata && officialMetadata.length > 0) {
      const p = (selectedPlatform || '').toLowerCase();
      officialMetadata.forEach(item => {
        const id = item.model_id;
        if (!id) return;
        const lower = id.toLowerCase();
        let matchesPlatform = false;
        if (
          p === 'openai' &&
          (lower.startsWith('gpt-') ||
            lower.startsWith('o1') ||
            lower.startsWith('o3') ||
            lower.startsWith('text-embedding'))
        ) {
          matchesPlatform = true;
        } else if (p === 'anthropic' && lower.startsWith('claude')) {
          matchesPlatform = true;
        } else if (p === 'gemini' && lower.startsWith('gemini')) {
          matchesPlatform = true;
        } else if (p === 'deepseek' && lower.startsWith('deepseek')) {
          matchesPlatform = true;
        } else if (p === 'zhipu' && (lower.startsWith('glm') || lower.startsWith('zai-'))) {
          matchesPlatform = true;
        } else if (p === 'qwen' && lower.startsWith('qwen')) {
          matchesPlatform = true;
        }
        if (matchesPlatform) {
          set.add(id);
        }
      });
    }

    return Array.from(set).map(m => ({ label: m, value: m }));
  }, [currentModels, upstreamDiscoveredModels, officialMetadata, selectedPlatform]);

  const upstreamTargetOptions = React.useMemo(() => {
    const set = new Set<string>();
    (upstreamDiscoveredModels || []).forEach(m => {
      if (m) set.add(m.trim());
    });
    (currentModels || []).forEach(m => {
      if (m) set.add(m.trim());
    });
    mappingRows.forEach(r => {
      if (r.from) set.add(r.from.trim());
      if (r.to) set.add(r.to.trim());
    });
    (modelSelectOptions || []).forEach(o => {
      if (o.value) set.add(o.value.trim());
    });
    return Array.from(set).map(m => ({ label: m, value: m }));
  }, [upstreamDiscoveredModels, currentModels, mappingRows, modelSelectOptions]);

  const handleRemoveMappingTargets = () => {
    const conflictSet = new Set(conflictingTargets);
    const nextModels = currentModels.filter(m => !conflictSet.has(m));
    const clientAliases = mappingRows.map(r => r.from.trim()).filter(Boolean);
    const merged = Array.from(new Set([...nextModels, ...clientAliases]));
    form.setFieldValue('models', merged);
    message.success(
      t(
        'providers.mapping_targets_removed',
        'Successfully removed mapping targets from model list',
      ),
    );
  };

  const handleFillRelevant = () => {
    const p = selectedPlatform || 'openai';
    const preset = PLATFORM_PRESETS[p]?.relevant || [];
    const merged = Array.from(new Set([...currentModels, ...preset]));
    form.setFieldValue('models', merged);
  };

  const handleFillAll = () => {
    if (upstreamDiscoveredModels.length > 0) {
      const merged = Array.from(new Set([...currentModels, ...upstreamDiscoveredModels]));
      form.setFieldValue('models', merged);
      return;
    }
    const p = selectedPlatform || 'openai';
    const preset = PLATFORM_PRESETS[p]?.all || [];
    const merged = Array.from(new Set([...currentModels, ...preset]));
    form.setFieldValue('models', merged);
  };

  const [isFetchingUpstream, setIsFetchingUpstream] = useState(false);
  const handleFetchUpstream = async () => {
    if (!editingProvider) {
      if (saveMutation.isPending) return;
      try {
        const values = await form.validateFields();
        saveMutation.mutate({ ...values, discover_models: true });
      } catch {
        // Form validation highlights the missing name or authorization result.
      }
      return;
    }
    setIsFetchingUpstream(true);
    try {
      if (editingProvider) {
        const res = await apiClient.post(`/admin/providers/${editingProvider.id}/discover-models`);
        const items = res.data?.data?.items || [];
        const discoveredNames = items.map((m: any) => m.model_name);
        if (discoveredNames.length > 0) {
          setUpstreamDiscoveredModels(discoveredNames);
          const merged = Array.from(new Set([...currentModels, ...discoveredNames]));
          form.setFieldValue('models', merged);
          message.success(
            t('providers.discover_success', 'Successfully discovered {count} models!').replace(
              '{count}',
              String(discoveredNames.length),
            ),
          );
        } else {
          message.warning('No models discovered');
        }
      } else {
        message.info(
          t(
            'providers.fetch_upstream_tip',
            'Please save provider first to fetch upstream models, or add models manually',
          ),
        );
      }
    } catch (err: any) {
      if (err?.configurationFeedbackHandled) return;
      window.dispatchEvent(new CustomEvent('bos:configuration-error', { detail: err }));
    } finally {
      setIsFetchingUpstream(false);
    }
  };

  const handleCopyAll = async () => {
    if (currentModels.length === 0) return;
    if (await copyText(currentModels.join(', '))) {
      message.success(t('providers.copied_to_clipboard', 'Copied to clipboard'));
    } else {
      message.warning(t('common.copy_failed'));
    }
  };

  const handleClearAll = () => {
    form.setFieldValue('models', []);
  };

  const handleEdit = (record: Provider) => {
    setEditingProvider(record);
    form.resetFields();
    const mappingStr =
      record.model_mapping && Object.keys(record.model_mapping).length > 0
        ? JSON.stringify(record.model_mapping, null, 2)
        : '';
    setMappingMode('visual');
    if (record.model_mapping && Object.keys(record.model_mapping).length > 0) {
      const rows = Object.entries(record.model_mapping).map(([from, to], i) => ({
        id: `m-${Date.now()}-${i}`,
        from,
        to: String(to),
      }));
      setMappingRows(rows);
      setMappingJsonText(mappingStr);
    } else {
      setMappingRows([]);
      setMappingJsonText('');
    }
    form.setFieldsValue({
      name: record.name,
      platform: record.platform,
      account_type: record.account_type || 'apikey',
      api_format: record.api_format || 'openai_chat',
      base_url: record.base_url,
      priority: record.priority || 0,
      concurrency_limit: record.concurrency_limit,
      schedulable: record.schedulable ?? true,
      rate_limit_rpm: record.rate_limit_rpm,
      price_input: defaultPriceConfirmed(record.pricing) ? (record.pricing?.input ?? 0) : undefined,
      price_output: defaultPriceConfirmed(record.pricing)
        ? (record.pricing?.output ?? 0)
        : undefined,
      price_cache_read: defaultPriceConfirmed(record.pricing)
        ? (record.pricing?.cache_read ?? 0)
        : undefined,
      price_cache_write: defaultPriceConfirmed(record.pricing)
        ? (record.pricing?.cache_write ?? 0)
        : undefined,
      model_mapping: mappingStr,
      models: [],
    });
    form.setFields(Object.keys(form.getFieldsValue(true)).map(name => ({ name, touched: false })));
    setModalVisible(true);
    apiClient
      .get(`/admin/providers/${record.id}/models`, {
        params: { page: 1, page_size: 100, status: 'active' },
      })
      .then(res => {
        const items = res.data?.data?.items || [];
        const modelNames = items.map((m: any) => m.model_name);
        form.setFieldValue('models', modelNames);
        setUpstreamDiscoveredModels(modelNames);
      })
      .catch(() => {});
  };

  const handleAdd = () => {
    setEditingProvider(null);
    setUpstreamDiscoveredModels([]);
    setMappingMode('visual');
    setMappingRows([]);
    setMappingJsonText('');
    form.resetFields();
    form.setFieldsValue({
      platform: 'openai',
      account_type: 'oauth',
      ...SUBSCRIPTION_DEFAULTS.openai,
      priority: 0,
      schedulable: true,
      price_input: undefined,
      price_output: undefined,
      price_cache_read: undefined,
      price_cache_write: undefined,
      models: [],
      model_mapping: '',
    });
    form.setFields(Object.keys(form.getFieldsValue(true)).map(name => ({ name, touched: false })));
    setModalVisible(true);
  };

  const guidedSetup = useFirstKeyAction(
    ['create-provider', 'edit-provider', 'test-provider'],
    !!currentUser,
    (action, _organizationID, providerID) => {
      if (action === 'create-provider' && isSuperAdmin) {
        handleAdd();
      } else if (action === 'test-provider' && providerID) {
        Modal.confirm({
          title: t('ux.test'),
          content: t('ux.test_note'),
          onOk: () => testConnectivity(providerID),
        });
      } else if (providerID) {
        void apiClient
          .get(`/admin/providers/${providerID}`)
          .then(response => {
            const provider = response.data.data as Provider;
            if (canModifyProvider(provider)) handleEdit(provider);
          })
          .catch(() => {});
      }
    },
  );

  const openModelsDrawer = (record: Provider) => {
    if (savingModel.current) return;
    setSelectedProvider(record);
    setModelsDrawerVisible(true);
  };

  const columns = [
    {
      title: t('providers.col_provider', 'Provider Name'),
      dataIndex: 'name',
      key: 'name',
      width: 160,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (val: string) => <RecordTitle name={val || '-'} />,
    },
    {
      title: t('home.col_platform', 'Platform'),
      dataIndex: 'platform',
      key: 'platform',
      width: 110,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (val: string) => (
        <Tag color="blue" style={{ margin: 0 }}>
          {val}
        </Tag>
      ),
    },
    {
      title: t('common.type', 'Type & Format'),
      key: 'type_format',
      width: 190,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, r: Provider) => (
        <Space size={4} wrap={false} style={{ whiteSpace: 'nowrap' }}>
          <Tag color="cyan" style={{ margin: 0, whiteSpace: 'nowrap' }}>
            {r.account_type || 'apikey'}
          </Tag>
          <Tag color="purple" style={{ margin: 0, whiteSpace: 'nowrap' }}>
            {r.api_format || 'openai_chat'}
          </Tag>
        </Space>
      ),
    },
    {
      title: t('providers.col_base_url', 'Base URL'),
      dataIndex: 'base_url',
      key: 'base_url',
      width: 280,
      ellipsis: true,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (url: string) => (
        <Text code ellipsis={{ tooltip: url }} style={{ maxWidth: '100%' }}>
          {url}
        </Text>
      ),
    },
    {
      title: t('providers.col_input_price', 'Default Price / 1M'),
      key: 'pricing',
      width: 240,
      render: (_: any, r: Provider) => {
        const inp = r.pricing?.input ?? 0;
        const out = r.pricing?.output ?? 0;
        const cacheRead = r.pricing?.cache_read ?? 0;
        const cacheWrite = r.pricing?.cache_write ?? 0;
        return (
          <Space direction="vertical" size={0}>
            <Tag color={defaultPriceConfirmed(r.pricing) ? 'default' : 'warning'}>
              {t(defaultPriceConfirmed(r.pricing) ? 'qa.price_default' : 'qa.price_unknown')}
            </Tag>
            <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
              In: {formatCurrency(inp, { decimals: 4 })} | Out:{' '}
              {formatCurrency(out, { decimals: 4 })}
            </Text>
            <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
              Cache In: {formatCurrency(cacheRead, { decimals: 4 })} | Cache Out:{' '}
              {formatCurrency(cacheWrite, { decimals: 4 })}
            </Text>
          </Space>
        );
      },
    },
    {
      title: t('common.status', 'Status'),
      dataIndex: 'status',
      key: 'status',
      width: 125,
      render: (val: string, record: Provider) => {
        const verified =
          !!record.verified_at &&
          !!record.updated_at &&
          Date.parse(record.verified_at) >= Date.parse(record.updated_at);
        return (
          <Space direction="vertical" size={4}>
            <Tag
              color={val === 'active' ? 'success' : val === 'rate_limited' ? 'warning' : 'error'}
              style={{ margin: 0 }}
            >
              {t(`ux.${val}`, val)}
            </Tag>
            <Tooltip title={t('onboarding.verification_note')}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t(verified ? 'onboarding.verified' : 'onboarding.unverified')}
              </Text>
            </Tooltip>
          </Space>
        );
      },
    },
    {
      title: t('providers.col_scheduling', '优先级与并发'),
      key: 'scheduling',
      width: 175,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, r: Provider) => (
        <Tooltip
          title={`${t('providers.priority_label')}: ${r.priority} · ${t('providers.concurrency_limit_label')}: ${r.concurrency_limit || t('providers.unlimited')}`}
        >
          <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
            {t('providers.priority')}: {r.priority} | {t('providers.concurrency')}:{' '}
            {r.concurrency_limit || '∞'}
          </Text>
        </Tooltip>
      ),
    },
  ];

  if (isError) {
    const statusHttp = (error as any)?.response?.status;
    const isForbidden = statusHttp === 403;

    return (
      <div style={{ padding: 24 }}>
        <Card>
          <Result
            status={isForbidden ? '403' : 'warning'}
            title={
              isForbidden
                ? t('routes.forbidden_title', '403 Forbidden')
                : t('common.error', 'Failed to Load Providers')
            }
            subTitle={
              isForbidden
                ? t(
                    'routes.forbidden_sub',
                    'You do not have permission to view Upstream Providers & Models. Platform Admin role is required.',
                  )
                : t(
                    'common.error',
                    'Unable to retrieve providers from gateway. Please check your network or try again.',
                  )
            }
            extra={
              <Button type="primary" onClick={() => refetch()}>
                {t('common.retry', 'Retry')}
              </Button>
            }
          />
        </Card>
      </div>
    );
  }

  return (
    <Card
      title={t('providers.title', 'Upstream Providers & Model Discovery')}
      extra={
        <Space>
          <Select
            allowClear
            placeholder={t('common.filter', 'Platform filter')}
            style={{ width: 150 }}
            value={platformFilter}
            onChange={v => {
              setPlatformFilter(v);
              setPage(1);
            }}
            options={[
              { label: 'OpenAI', value: 'openai' },
              { label: 'Anthropic', value: 'anthropic' },
              { label: 'Gemini', value: 'gemini' },
              { label: 'DeepSeek', value: 'deepseek' },
              { label: 'Grok', value: 'grok' },
              { label: 'Kimi', value: 'kimi' },
              { label: 'Zhipu', value: 'zhipu' },
              { label: 'Minimax', value: 'minimax' },
              { label: 'Antigravity', value: 'antigravity' },
              { label: 'Composite', value: 'composite' },
            ]}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={handleAdd}>
            {t('providers.add_provider', 'Add Provider')}
          </Button>
        </Space>
      }
    >
      <Table
        rowKey="id"
        dataSource={providersData?.items || []}
        columns={[
          columns[0],
          columns[1],
          columns[2],
          columns[3],
          columns[6],
          {
            title: t('common.actions'),
            key: 'actions',
            width: 175,
            fixed: 'right' as const,
            onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
            onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
            render: (_: any, record: Provider) => (
              <Space size={4} wrap={false} style={{ whiteSpace: 'nowrap' }}>
                <TableAction size="small" onClick={() => openModelsDrawer(record)}>
                  {t('providers.btn_models')}
                </TableAction>
                {canModifyProvider(record) && (
                  <TableAction size="small" onClick={() => handleEdit(record)}>
                    {t('common.edit')}
                  </TableAction>
                )}
                <TableActionMenu
                  menu={{
                    items: [
                      { key: 'details', label: t('ux.details') },
                      {
                        key: 'test',
                        label: t('ux.test'),
                        disabled: !canModifyProvider(record) || testingId !== null,
                      },
                      {
                        key: 'scan',
                        label: t('ux.scan'),
                        disabled: !canModifyProvider(record) || discoveringId !== null,
                      },
                      { type: 'divider' },
                      {
                        key: 'status',
                        label: t(
                          record.status === 'active'
                            ? 'providers.btn_disable'
                            : 'providers.btn_enable',
                        ),
                        disabled: !canModifyProvider(record) || toggleStatusMutation.isPending,
                      },
                      {
                        key: 'delete',
                        label: t('common.delete'),
                        danger: true,
                        disabled: !canModifyProvider(record) || deleteMutation.isPending,
                      },
                    ],
                    onClick: ({ key }) => {
                      if (key === 'details') return setDetail(record);
                      const action =
                        key === 'test'
                          ? t('ux.test')
                          : key === 'scan'
                            ? t('ux.scan')
                            : key === 'delete'
                              ? t('common.delete')
                              : t(
                                  record.status === 'active'
                                    ? 'providers.btn_disable'
                                    : 'providers.btn_enable',
                                );
                      Modal.confirm({
                        title: `${action} · ${record.name}`,
                        content: t(
                          key === 'test'
                            ? 'ux.test_note'
                            : key === 'scan'
                              ? 'ux.scan_note'
                              : key === 'delete'
                                ? 'providers.delete_confirm'
                                : record.status === 'active'
                                  ? 'providers.disable_confirm'
                                  : 'providers.enable_confirm',
                        ),
                        okText: action,
                        cancelText: t('common.cancel'),
                        okButtonProps: { danger: key === 'delete' },
                        onOk: () =>
                          key === 'test'
                            ? testConnectivity(record.id)
                            : key === 'scan'
                              ? discoverModels(record.id)
                              : key === 'delete'
                                ? deleteMutation.mutateAsync(record.id)
                                : toggleStatusMutation.mutateAsync({
                                    id: record.id,
                                    status: record.status === 'active' ? 'disabled' : 'active',
                                  }),
                      });
                    },
                  }}
                />
              </Space>
            ),
          },
        ]}
        loading={isLoading}
        scroll={{ x: 1090 }}
        pagination={{
          current: page,
          pageSize,
          total: providersData?.total || 0,
          onChange: (p, ps) => {
            setPage(p);
            setPageSize(ps);
          },
        }}
      />

      <PageHelp note="ux.enabled_not_healthy" />
      <Drawer
        title={detail?.name}
        open={!!detail}
        onClose={() => setDetail(null)}
        width="min(620px, 100vw)"
        className="ux-details"
      >
        {detail && (
          <RecordDetails
            items={columns
              .filter(column => column.key !== 'actions')
              .map(column => ({
                key: column.key,
                label: column.title,
                children:
                  (column.render as any)?.(
                    (column as any).dataIndex
                      ? (detail as any)[(column as any).dataIndex]
                      : undefined,
                    detail,
                  ) ??
                  (detail as any)[(column as any).dataIndex] ??
                  '-',
              }))}
          />
        )}
      </Drawer>
      {/* Provider Form Modal */}
      <Modal
        className="console-form-modal"
        title={
          editingProvider
            ? `${t('providers.edit_provider', 'Edit Provider')} · ${editingProvider.name}`
            : t('providers.add_provider', 'Add Upstream Provider')
        }
        open={modalVisible}
        maskClosable={false}
        onOk={() => form.submit()}
        onCancel={() => {
          if (!saveMutation.isPending) cancel();
        }}
        okText={
          !editingProvider && !currentModels.length
            ? t('providers.subscription_save_and_discover')
            : t('ux.save_channel')
        }
        confirmLoading={saveMutation.isPending}
        width={720}
      >
        {!isSubscription && <PageHelp note="ux.provider_note" />}
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          form={form}
          scrollToFirstError={{ block: 'center', focus: true }}
          layout="vertical"
          labelWrap
          disabled={saveMutation.isPending}
          onValuesChange={changed => {
            if ('platform' in changed || 'account_type' in changed) {
              form.setFieldValue('oauth_authorization', undefined);
              if (form.getFieldValue('account_type') === 'oauth') {
                const defaults = SUBSCRIPTION_DEFAULTS[form.getFieldValue('platform')];
                if (defaults) form.setFieldsValue({ ...defaults, api_key: '' });
                else
                  form.setFieldsValue({
                    account_type: 'apikey',
                    ...PLATFORM_DEFAULTS[form.getFieldValue('platform')],
                  });
              } else if ('account_type' in changed) {
                form.setFieldsValue(PLATFORM_DEFAULTS[form.getFieldValue('platform')] || {});
              }
            }
          }}
          onFinish={values => {
            if (!saveMutation.isPending) saveMutation.mutate(values);
          }}
        >
          <Form.Item
            name="name"
            label={t('providers.name_label', 'Provider Name')}
            rules={[
              {
                required: true,
                message: t('providers.name_required', 'Please enter provider name'),
              },
            ]}
          >
            <Input
              placeholder={t(
                'providers.name_placeholder',
                'e.g. OpenAI Main API, DeepSeek Production',
              )}
            />
          </Form.Item>

          <Row gutter={16}>
            <Col xs={24} sm={isSubscription ? 12 : 8}>
              <Form.Item
                name="platform"
                label={t('home.col_platform', 'Platform')}
                rules={[{ required: true }]}
              >
                <Select
                  disabled={editingProvider?.account_type === 'oauth'}
                  onChange={handlePlatformChange}
                  options={[
                    { label: 'OpenAI', value: 'openai' },
                    { label: 'Anthropic', value: 'anthropic' },
                    { label: 'Gemini', value: 'gemini' },
                    { label: 'DeepSeek', value: 'deepseek' },
                    { label: 'Grok', value: 'grok' },
                    { label: 'Kimi', value: 'kimi' },
                    { label: 'Zhipu', value: 'zhipu' },
                    { label: 'Minimax', value: 'minimax' },
                    { label: 'Antigravity', value: 'antigravity' },
                    { label: 'Composite', value: 'composite' },
                  ]}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={isSubscription ? 12 : 8}>
              <Form.Item
                name="account_type"
                label={t('providers.account_type', 'Account Type')}
                rules={[{ required: true }]}
              >
                <Select
                  disabled={editingProvider?.account_type === 'oauth'}
                  options={[
                    { label: t('providers.type_apikey', 'API Key'), value: 'apikey' },
                    {
                      label: t('providers.type_oauth', 'OAuth'),
                      value: 'oauth',
                      disabled:
                        !SUBSCRIPTION_DEFAULTS[selectedPlatform] ||
                        (!!editingProvider && editingProvider.account_type !== 'oauth'),
                    },
                    { label: t('providers.type_upstream', 'Upstream'), value: 'upstream' },
                  ]}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={8} style={isSubscription ? { display: 'none' } : undefined}>
              <Form.Item
                name="api_format"
                label={t('providers.api_format', 'API Format')}
                rules={[{ required: true }]}
              >
                <Select
                  disabled={isSubscription}
                  options={[
                    { label: 'OpenAI Chat', value: 'openai_chat' },
                    { label: 'OpenAI Responses', value: 'openai_responses' },
                    { label: 'Anthropic Messages', value: 'anthropic_messages' },
                    { label: 'Gemini Native', value: 'gemini' },
                    { label: 'Realtime', value: 'realtime' },
                    { label: 'Custom', value: 'custom' },
                  ]}
                />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item
            name="base_url"
            hidden={isSubscription}
            label={t('providers.col_base_url', 'Base URL')}
            rules={[
              { required: true, message: t('providers.base_url_required', 'Base URL required') },
            ]}
          >
            <Input
              disabled={isSubscription}
              placeholder={
                PLATFORM_DEFAULTS[selectedPlatform]?.base_url || 'https://your-upstream.example.com'
              }
            />
          </Form.Item>

          {!isSubscription && SUBSCRIPTION_DEFAULTS[selectedPlatform] && !editingProvider && (
            <Alert
              style={{ marginBottom: 16 }}
              type="info"
              showIcon
              message={t(
                'providers.subscription_switch_help',
                'Using a subscription account? Generate a sign-in link instead of entering an API key.',
              )}
              action={
                <Button
                  size="small"
                  onClick={() =>
                    form.setFieldsValue({
                      account_type: 'oauth',
                      ...SUBSCRIPTION_DEFAULTS[selectedPlatform],
                      api_key: '',
                      oauth_authorization: undefined,
                    })
                  }
                >
                  {t('providers.subscription_switch', 'Use official account sign-in')}
                </Button>
              }
            />
          )}

          {isSubscription ? (
            <Form.Item
              name="oauth_authorization"
              validateTrigger={[]}
              rules={[
                {
                  validator: async (_, value) => {
                    if (
                      (!editingProvider || value?.session_id) &&
                      (!value?.callback?.trim() || Date.parse(value.expires_at) <= Date.now())
                    ) {
                      throw new Error(
                        t(
                          'providers.subscription_required',
                          'Complete official sign-in before saving.',
                        ),
                      );
                    }
                  },
                },
              ]}
            >
              <SubscriptionAuthorization
                key={selectedPlatform}
                platform={selectedPlatform}
                connected={!!editingProvider}
              />
            </Form.Item>
          ) : (
            <Form.Item
              name="api_key"
              label={
                editingProvider
                  ? t(
                      'providers.api_key_update_label',
                      'Update API Key (Leave blank to keep current)',
                    )
                  : t('providers.api_key_label', 'API Key')
              }
            >
              <Input.Password placeholder="sk-..." />
            </Form.Item>
          )}

          <AdvancedFields key={`${guidedSetup}:${isSubscription}`} defaultOpen={false}>
            <Row gutter={16}>
              <Col xs={24} sm={8}>
                <Form.Item
                  name="priority"
                  label={t('providers.priority_label', 'Scheduling Priority')}
                >
                  <InputNumber style={{ width: '100%' }} />
                </Form.Item>
              </Col>
              <Col xs={24} sm={8}>
                <Form.Item
                  name="concurrency_limit"
                  label={t('providers.concurrency_limit_label', 'Concurrency Limit')}
                >
                  <InputNumber
                    min={1}
                    style={{ width: '100%' }}
                    placeholder={t('providers.unlimited', 'Unlimited')}
                  />
                </Form.Item>
              </Col>
              <Col xs={24} sm={8}>
                <Form.Item
                  name="rate_limit_rpm"
                  label={t('providers.rate_limit_rpm_label', 'Rate Limit RPM')}
                >
                  <InputNumber
                    min={1}
                    style={{ width: '100%' }}
                    placeholder={t('providers.unlimited', 'Unlimited')}
                  />
                </Form.Item>
              </Col>
            </Row>

            {/* Models and Model Mapping Section */}
            <Divider
              orientation="left"
              style={{ margin: '14px 0 14px 0', fontSize: 13, color: '#888' }}
            >
              {t('providers.models_label', '模型')} &{' '}
              {t('providers.model_mapping_label', '模型映射')}
            </Divider>

            <Form.Item
              name="models"
              extra={
                isSubscription
                  ? t(
                      'providers.subscription_models_help',
                      'Models may be left empty. Paste the authorization result and choose Save Channel below; BOS will save the account, then try to discover its models. If none are returned, add models manually before using the channel.',
                    )
                  : undefined
              }
              label={
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    width: '100%',
                  }}
                >
                  <span>{t('providers.models_label', '模型')}</span>
                  <span style={{ fontSize: 12, color: '#8c8c8c', fontWeight: 'normal' }}>
                    {t('providers.models_selected_count', '已选 {count} 个').replace(
                      '{count}',
                      String(currentModels.length),
                    )}
                  </span>
                </div>
              }
              style={{ marginBottom: 10 }}
            >
              <Select
                mode="tags"
                style={{ width: '100%' }}
                placeholder={t(
                  'providers.models_placeholder',
                  '输入模型名称，按回车或逗号分隔添加',
                )}
                tokenSeparators={[',']}
                options={modelSelectOptions}
              />
            </Form.Item>

            {conflictingTargets.length > 0 && (
              <Alert
                type="warning"
                showIcon
                style={{ marginBottom: 12 }}
                message={
                  <div
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      flexWrap: 'wrap',
                      gap: 8,
                    }}
                  >
                    <span style={{ fontSize: 13 }}>
                      {t(
                        'providers.mapping_conflict_tip',
                        '映射的上游模型 "{targets}" 也在此处列出。将它们从模型中移除，以保持 `/v1/models` 响应对用户友好并隐藏供应商特定的名称。',
                      ).replace('{targets}', conflictingTargets.join('", "'))}
                    </span>
                    <Button size="small" danger onClick={handleRemoveMappingTargets}>
                      {t('providers.btn_remove_mapping_targets', '移除映射目标')}
                    </Button>
                  </div>
                }
              />
            )}

            <div style={{ marginBottom: 16 }}>
              <Space wrap size={[8, 8]}>
                <Button size="small" onClick={handleFillRelevant}>
                  {t('providers.btn_fill_relevant', '填入相关模型')}
                </Button>
                <Button size="small" onClick={handleFillAll}>
                  {t('providers.btn_fill_all', '+ 填充所有模型')}
                </Button>
                <Button
                  size="small"
                  icon={<CompassOutlined />}
                  loading={isFetchingUpstream || saveMutation.isPending}
                  onClick={handleFetchUpstream}
                >
                  {!editingProvider
                    ? t('providers.subscription_save_and_discover', 'Save and discover models')
                    : t('providers.btn_fetch_upstream', '从上游获取')}
                </Button>
                <Button size="small" icon={<CopyOutlined />} onClick={handleCopyAll}>
                  {t('providers.btn_copy_all', '全部复制')}
                </Button>
                <Button size="small" danger icon={<ClearOutlined />} onClick={handleClearAll}>
                  {t('providers.btn_clear_all', '清除全部')}
                </Button>
              </Space>
            </div>

            {/* Model Mapping Section */}
            <div style={{ marginBottom: 16 }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  marginBottom: 4,
                }}
              >
                <Space size={6}>
                  <span style={{ fontSize: 14, fontWeight: 500, color: '#262626' }}>
                    {t('providers.model_mapping_label', '模型映射')}
                  </span>
                  <Tooltip
                    title={t(
                      'providers.model_mapping_tooltip',
                      '使用 JSON 格式配置模型映射关系。当用户请求左侧的模型时，网关将自动替换为右侧的上游实际模型。例如: {"glm-4": "zai-org/GLM-5.3-Flash"}',
                    )}
                  >
                    <QuestionCircleOutlined
                      style={{ color: '#8c8c8c', cursor: 'pointer', fontSize: 13 }}
                    />
                  </Tooltip>
                </Space>
              </div>
              <div style={{ fontSize: 12, color: '#8c8c8c', marginBottom: 10 }}>
                {t(
                  'providers.model_mapping_desc',
                  '将请求模型名称映射到实际提供商模型名称 (JSON 格式)',
                )}
              </div>

              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  flexWrap: 'wrap',
                  gap: 8,
                  marginBottom: 12,
                }}
              >
                <SegmentedControl
                  aria-label={t('providers.model_mapping_label')}
                  value={mappingMode}
                  onChange={val => handleModeChange(val as 'visual' | 'json')}
                  options={[
                    {
                      label: t('providers.mode_visual', '可视化'),
                      value: 'visual',
                      icon: <TableOutlined />,
                    },
                    {
                      label: t('providers.mode_json', 'JSON'),
                      value: 'json',
                      icon: <CodeOutlined />,
                    },
                  ]}
                />
                <Space wrap size={12}>
                  <Button type="link" size="small" style={{ padding: 0 }} onClick={handleMapAll}>
                    {t('providers.btn_map_all', '一键映射所有')}
                  </Button>
                  <Button
                    type="link"
                    size="small"
                    style={{ padding: 0 }}
                    onClick={handleFillTemplate}
                  >
                    {t('providers.fill_template', '填入模版')}
                  </Button>
                  {(mappingRows.length > 0 || !!mappingJsonText.trim()) && (
                    <Popconfirm
                      title={t('providers.clear_mapping_confirm', '确定要清空所有模型映射吗？')}
                      onConfirm={handleClearAllMappings}
                      okText={t('common.confirm', '确定')}
                      cancelText={t('common.cancel', '取消')}
                    >
                      <Button type="link" danger size="small" style={{ padding: 0 }}>
                        {t('providers.btn_clear_mappings', '清空所有映射')}
                      </Button>
                    </Popconfirm>
                  )}
                </Space>
              </div>

              {mappingMode === 'visual' ? (
                <div
                  style={{
                    background: '#fafafa',
                    border: '1px solid #f0f0f0',
                    borderRadius: 8,
                    padding: 12,
                  }}
                >
                  {mappingRows.map(row => (
                    <Row
                      gutter={[8, 8]}
                      key={row.id}
                      style={{ marginBottom: 16, alignItems: 'end' }}
                    >
                      <Col xs={24} sm={11}>
                        <Form.Item
                          label={t('providers.original_model', '原始模型')}
                          style={{ marginBottom: 0 }}
                        >
                          <MappingModelSelect
                            options={upstreamTargetOptions}
                            placeholder={t(
                              'providers.original_model_placeholder',
                              '选择或输入原始模型',
                            )}
                            customPlaceholder={t(
                              'providers.custom_original_placeholder',
                              '输入自定义原始模型并回车',
                            )}
                            value={row.from}
                            onChange={val => handleUpdateRow(row.id, 'from', val)}
                          />
                        </Form.Item>
                      </Col>
                      <Col xs={20} sm={11}>
                        <Form.Item
                          label={t('providers.target_model', '替换模型')}
                          style={{ marginBottom: 0 }}
                        >
                          <MappingModelSelect
                            options={upstreamTargetOptions}
                            placeholder={t(
                              'providers.target_model_placeholder',
                              '选择替换目标模型',
                            )}
                            customPlaceholder={t(
                              'providers.custom_target_placeholder',
                              '输入自定义上游模型并回车',
                            )}
                            value={row.to}
                            onChange={val => handleUpdateRow(row.id, 'to', val)}
                          />
                        </Form.Item>
                      </Col>
                      <Col xs={4} sm={2} style={{ textAlign: 'center' }}>
                        <Button
                          type="text"
                          danger
                          icon={<DeleteOutlined />}
                          onClick={() => handleRemoveRow(row.id)}
                        />
                      </Col>
                    </Row>
                  ))}

                  <Button
                    type="dashed"
                    block
                    icon={<PlusOutlined />}
                    onClick={handleAddRow}
                    style={{ borderRadius: 6, marginTop: mappingRows.length > 0 ? 4 : 0 }}
                  >
                    {t('providers.add_mapping', '添加映射')}
                  </Button>
                </div>
              ) : (
                <div>
                  <Form.Item
                    name="model_mapping"
                    rules={[
                      {
                        validator: (_, value) => {
                          if (!value?.trim()) return Promise.resolve();
                          try {
                            const mapping = JSON.parse(value);
                            if (
                              mapping &&
                              typeof mapping === 'object' &&
                              !Array.isArray(mapping) &&
                              Object.values(mapping).every(item => typeof item === 'string')
                            )
                              return Promise.resolve();
                          } catch {
                            /* Show a local field error below. */
                          }
                          return Promise.reject(
                            new Error(t('providers.model_mapping_invalid_json')),
                          );
                        },
                      },
                    ]}
                  >
                    <Input.TextArea
                      rows={6}
                      value={mappingJsonText}
                      onChange={handleJsonChange}
                      placeholder={t('providers.model_mapping_placeholder')}
                      style={{ fontFamily: 'monospace', fontSize: 12, borderRadius: 6 }}
                    />
                  </Form.Item>
                  <div style={{ textAlign: 'right', marginTop: 4 }}>
                    <Button type="link" size="small" onClick={handleFormatMappingJson}>
                      {t('providers.format_json', '格式化 JSON')}
                    </Button>
                  </div>
                </div>
              )}
            </div>

            <Divider
              orientation="left"
              style={{ margin: '14px 0 14px 0', fontSize: 13, color: '#888', whiteSpace: 'normal' }}
            >
              {t('providers.default_pricing_divider', 'Default Pricing (per 1M Tokens)')}
            </Divider>
            <Typography.Paragraph type="secondary">{t('qa.price_confirm')}</Typography.Paragraph>
            <Row gutter={16}>
              <Col xs={24} sm={12}>
                <Form.Item
                  name="price_input"
                  dependencies={['price_output']}
                  rules={[
                    ({ getFieldValue }) => ({
                      validator: (_, value) =>
                        getFieldValue('price_output') == null || value != null
                          ? Promise.resolve()
                          : Promise.reject(new Error(t('qa.price_confirm'))),
                    }),
                  ]}
                  label={t('providers.price_input_label', 'Default Input Price / 1M ($)')}
                  tooltip={t(
                    'providers.price_input_tooltip',
                    'Default price charged per 1M input tokens. Used by models unless individually overridden.',
                  )}
                >
                  <InputNumber
                    min={0}
                    step={0.0001}
                    precision={4}
                    style={{ width: '100%' }}
                    prefix="$"
                    placeholder="0.0000"
                  />
                </Form.Item>
              </Col>
              <Col xs={24} sm={12}>
                <Form.Item
                  name="price_output"
                  dependencies={['price_input']}
                  rules={[
                    ({ getFieldValue }) => ({
                      validator: (_, value) =>
                        getFieldValue('price_input') == null || value != null
                          ? Promise.resolve()
                          : Promise.reject(new Error(t('qa.price_confirm'))),
                    }),
                  ]}
                  label={t('providers.price_output_label', 'Default Output Price / 1M ($)')}
                  tooltip={t(
                    'providers.price_output_tooltip',
                    'Default price charged per 1M output tokens. Used by models unless individually overridden.',
                  )}
                >
                  <InputNumber
                    min={0}
                    step={0.0001}
                    precision={4}
                    style={{ width: '100%' }}
                    prefix="$"
                    placeholder="0.0000"
                  />
                </Form.Item>
              </Col>
            </Row>

            <Row gutter={16}>
              <Col xs={24} sm={12}>
                <Form.Item
                  name="price_cache_read"
                  label={t('providers.price_cache_read_label', 'Default Cache Read Price / 1M ($)')}
                  tooltip={t(
                    'providers.price_cache_read_tooltip',
                    'Default price charged per 1M cached prefix tokens. Used by models unless individually overridden.',
                  )}
                >
                  <InputNumber
                    min={0}
                    step={0.0001}
                    precision={4}
                    style={{ width: '100%' }}
                    prefix="$"
                    placeholder="0.0000"
                  />
                </Form.Item>
              </Col>
              <Col xs={24} sm={12}>
                <Form.Item
                  name="price_cache_write"
                  label={t(
                    'providers.price_cache_write_label',
                    'Default Cache Write Price / 1M ($)',
                  )}
                  tooltip={t(
                    'providers.price_cache_write_tooltip',
                    'Default price charged per 1M cache write tokens. Used by models unless individually overridden.',
                  )}
                >
                  <InputNumber
                    min={0}
                    step={0.0001}
                    precision={4}
                    style={{ width: '100%' }}
                    prefix="$"
                    placeholder="0.0000"
                  />
                </Form.Item>
              </Col>
            </Row>

            <Form.Item
              name="schedulable"
              label={t('providers.schedulable_label', 'Schedulable for Inference')}
              valuePropName="checked"
            >
              <Switch defaultChecked />
            </Form.Item>
          </AdvancedFields>
        </Form>
      </Modal>

      {/* Discovered Models Drawer */}
      <Drawer
        title={`${t('providers.drawer_models_for', 'Discovered Models for')} ${selectedProvider?.name || ''}`}
        placement="right"
        width="min(1260px, 100vw)"
        styles={{ header: { flexWrap: 'wrap', gap: 12 } }}
        maskClosable={false}
        open={modelsDrawerVisible}
        onClose={() => {
          if (!savingModel.current) setModelsDrawerVisible(false);
        }}
        extra={
          <Space wrap>
            <Button
              size="small"
              disabled={!canModifyProvider(selectedProvider) || discoveringId !== null}
              onClick={() => {
                if (!selectedProvider) return;
                setModelsDrawerVisible(false);
                handleEdit(selectedProvider);
              }}
            >
              {t('providers.edit_provider', 'Edit Provider')}
            </Button>
            <Button
              size="small"
              icon={<CompassOutlined />}
              loading={discoveringId !== null}
              disabled={!canModifyProvider(selectedProvider)}
              onClick={() => selectedProvider && discoverModels(selectedProvider.id)}
            >
              {t('providers.discover_models', 'Discover Models')}
            </Button>
            {isSuperAdmin && (
              <Button
                size="small"
                icon={<SyncOutlined spin={isSyncingKb} />}
                loading={isSyncingKb}
                onClick={handleSyncKnowledgeBase}
              >
                {t('providers.btn_sync_kb', 'Sync Official Knowledge Base')}
              </Button>
            )}
          </Space>
        }
      >
        <Alert type="info" showIcon message={t('qa.price_note')} style={{ marginBottom: 16 }} />
        {!!discoveredModels?.length && (
          <Alert
            type="success"
            showIcon
            message={t('qa.authorize_next')}
            style={{ marginBottom: 16 }}
            description={
              <Typography.Link
                href="/admin/organizations"
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('qa.configure_access')}
              </Typography.Link>
            }
          />
        )}
        {modelsError && <LoadError retry={refetchModels} />}
        {selectedProvider?.account_type === 'oauth' &&
          !isLoadingModels &&
          discoveringId !== selectedProvider.id &&
          discoveredModels?.length === 0 && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message={t(
                'providers.subscription_models_pending',
                'The channel is saved, but no models are configured. Retry model discovery or choose Edit Provider to add models manually.',
              )}
            />
          )}
        <Table
          rowKey="model_name"
          dataSource={discoveredModels}
          loading={isLoadingModels || discoveringId === selectedProvider?.id}
          size="small"
          scroll={{ x: 1120 }}
          pagination={{ pageSize: 15 }}
          columns={[
            {
              title: t('providers.model_name', 'Model Name'),
              dataIndex: 'model_name',
              key: 'model_name',
              width: 290,
              render: (name: string) => (
                <code style={{ fontSize: 12, whiteSpace: 'nowrap' }}>{name}</code>
              ),
            },
            {
              title: t('providers.col_capabilities', 'Capabilities & Endpoints'),
              dataIndex: 'capabilities',
              key: 'capabilities',
              width: 380,
              render: (cap: any, record: DiscoveredModel) => (
                <ModelCapabilitiesTags capabilities={cap} modelName={record.model_name} />
              ),
            },
            {
              title: t('common.status', 'Status'),
              dataIndex: 'status',
              key: 'status',
              width: 80,
              align: 'center' as const,
              render: (s: string) => (
                <Tag color={s === 'active' ? 'success' : 'default'} style={{ margin: 0 }}>
                  {s === 'active'
                    ? t('providers.status_active', 'Active')
                    : t('providers.status_inactive', 'Inactive')}
                </Tag>
              ),
            },
            {
              title: t('providers.pricing_per_1m', 'Pricing / 1M'),
              key: 'pricing',
              width: 210,
              render: (_: any, record: DiscoveredModel) => {
                const inp = record.price_input_per_1m ?? 0;
                const out = record.price_output_per_1m ?? 0;
                const cacheRead = record.price_cache_read_per_1m ?? 0;
                const cacheWrite = record.price_cache_write_per_1m ?? 0;
                const source = modelPriceSource(selectedProvider?.pricing, record);
                return (
                  <Space direction="vertical" size={0}>
                    <Tag color={source === 'unknown' ? 'warning' : 'default'}>
                      {t(`qa.price_${source}`)}
                    </Tag>
                    <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                      In: {formatCurrency(inp, { decimals: 4 })} | Out:{' '}
                      {formatCurrency(out, { decimals: 4 })}
                    </Text>
                    <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                      Cache In: {formatCurrency(cacheRead, { decimals: 4 })} | Cache Out:{' '}
                      {formatCurrency(cacheWrite, { decimals: 4 })}
                    </Text>
                  </Space>
                );
              },
            },
            {
              title: `${t('providers.discovered_at', 'Discovered At')} (${timezoneBadge})`,
              dataIndex: 'discovered_at',
              key: 'discovered_at',
              width: 140,
              render: (t: string) => (
                <Text style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                  {t ? formatDateTime(t) : '-'}
                </Text>
              ),
            },
            {
              title: t('common.actions', 'Actions'),
              key: 'actions',
              width: 90,
              align: 'center' as const,
              onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
              onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
              render: (_: any, record: DiscoveredModel) => (
                <TableAction
                  size="small"
                  style={{ whiteSpace: 'nowrap' }}
                  disabled={!canModifyProvider(selectedProvider)}
                  onClick={() => {
                    if (savingModel.current) return;
                    setEditingModel(record);
                    const caps = record.capabilities || {};
                    modelPriceForm.setFieldsValue({
                      price_input_per_1m: record.price_input_per_1m || 0,
                      price_output_per_1m: record.price_output_per_1m || 0,
                      price_cache_read_per_1m: record.price_cache_read_per_1m || 0,
                      price_cache_write_per_1m: record.price_cache_write_per_1m || 0,
                      status: record.status || 'active',
                      supports_vision: Boolean(caps.vision),
                      supports_video: Boolean(caps.video),
                      supports_tools: caps.tools !== undefined ? Boolean(caps.tools) : true,
                      supports_reasoning: Boolean(caps.reasoning),
                      max_input_tokens: caps.max_input_tokens || 32000,
                    });
                    setModelPriceModalVisible(true);
                  }}
                >
                  {t('providers.btn_price', 'Configure')}
                </TableAction>
              ),
            },
          ]}
        />
      </Drawer>

      {/* Edit Model Price & Capabilities Modal */}
      <Modal
        className="console-form-modal"
        title={`${t('providers.edit_model_pricing_title', 'Edit Model Config & Pricing:')} ${editingModel?.model_name || ''}`}
        open={modelPriceModalVisible}
        maskClosable={false}
        onOk={() => modelPriceForm.submit()}
        onCancel={() => {
          if (savingModel.current) return;
          setModelPriceModalVisible(false);
          setEditingModel(null);
        }}
        confirmLoading={updateModelMutation.isPending}
        width={520}
      >
        <Form
          onFinishFailed={() =>
            window.dispatchEvent(
              new CustomEvent('bos:configuration-error', {
                detail: { response: { data: { error: { code: 'field_validation' } } } },
              }),
            )
          }
          form={modelPriceForm}
          layout="vertical"
          labelWrap
          disabled={updateModelMutation.isPending}
          onFinish={values => {
            if (savingModel.current || !selectedProvider || !editingModel) return;
            savingModel.current = true;
            updateModelMutation.mutate(values);
          }}
        >
          <Form.Item label={t('providers.model_name', 'Model Name')}>
            <Input value={editingModel?.model_name} disabled />
          </Form.Item>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item
                name="price_input_per_1m"
                label={t('providers.input_price_1m', 'Input Price / 1M ($)')}
                rules={[
                  {
                    required: true,
                    message: t('providers.enter_input_price', 'Please enter input price'),
                  },
                ]}
              >
                <InputNumber
                  min={0}
                  step={0.0001}
                  precision={4}
                  style={{ width: '100%' }}
                  prefix="$"
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item
                name="price_output_per_1m"
                label={t('providers.output_price_1m', 'Output Price / 1M ($)')}
                rules={[
                  {
                    required: true,
                    message: t('providers.enter_output_price', 'Please enter output price'),
                  },
                ]}
              >
                <InputNumber
                  min={0}
                  step={0.0001}
                  precision={4}
                  style={{ width: '100%' }}
                  prefix="$"
                />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item
                name="price_cache_read_per_1m"
                label={t('providers.cache_read_price_1m', 'Cache Read Price / 1M ($)')}
                rules={[
                  {
                    required: true,
                    message: t('providers.enter_cache_read_price', 'Please enter cache read price'),
                  },
                ]}
              >
                <InputNumber
                  min={0}
                  step={0.0001}
                  precision={4}
                  style={{ width: '100%' }}
                  prefix="$"
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item
                name="price_cache_write_per_1m"
                label={t('providers.cache_write_price_1m', 'Cache Write Price / 1M ($)')}
                rules={[
                  {
                    required: true,
                    message: t(
                      'providers.enter_cache_write_price',
                      'Please enter cache write price',
                    ),
                  },
                ]}
              >
                <InputNumber
                  min={0}
                  step={0.0001}
                  precision={4}
                  style={{ width: '100%' }}
                  prefix="$"
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="status" label={t('common.status', 'Status')}>
            <Select
              options={[
                { label: t('providers.status_active', 'Active'), value: 'active' },
                { label: t('providers.status_inactive', 'Inactive'), value: 'inactive' },
              ]}
            />
          </Form.Item>

          <Divider orientation="left" style={{ margin: '16px 0 12px' }}>
            <Text type="secondary" style={{ fontSize: 13 }}>
              {t('providers.cap_section_title', 'Model Capabilities Customization')}
            </Text>
          </Divider>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item
                name="supports_vision"
                label={t('providers.cap_vision_label', 'Supports Vision/Image')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item
                name="supports_video"
                label={t('providers.cap_video_label', 'Supports Video')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item
                name="supports_tools"
                label={t('providers.cap_tools_label', 'Supports Function Calling')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item
                name="supports_reasoning"
                label={t('providers.cap_reasoning_label', 'Reasoning/Thinking Model')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name="max_input_tokens"
            label={t('providers.cap_max_tokens_label', 'Context Length (Tokens)')}
          >
            <InputNumber min={0} step={1000} style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
};
