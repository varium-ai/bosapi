import { Alert, Button, Form, Select, Spin, Typography } from 'antd';
import { useQueries, useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { firstKeyPath } from '../onboarding/firstKey';
import { LoadError } from '../common/ConsoleUX';
import {
  buildKeyModelOptions,
  keyModelChannels,
  loadKeyChannelModels,
  type KeyChannelOption,
} from './keyModelOptions';
import './key-access.css';

function useKeyAccessOptions({
  enabled,
  organizationID,
}: {
  enabled: boolean;
  organizationID?: number;
}) {
  const form = Form.useFormInstance();
  const selectedChannels = Form.useWatch('allowed_provider_ids', form) as number[] | undefined;
  const selectedModels = Form.useWatch('allowed_models', form) as string[] | undefined;
  const channels = useQuery({
    queryKey: ['key-channel-options', organizationID],
    enabled: enabled && !!organizationID,
    queryFn: async () => {
      const items: KeyChannelOption[] = [];
      for (let page = 1; ; page++) {
        const response = await apiClient.get('/admin/providers', {
          params: { organization_id: organizationID, page, page_size: 100, status: 'active' },
        });
        const data = response.data.data;
        items.push(...data.items);
        if (!data.items.length || items.length >= data.total) return items;
      }
    },
  });
  const modelChannels = keyModelChannels(channels.data || [], selectedChannels);
  const modelQueries = useQueries({
    queries: modelChannels.map(channel => ({
      queryKey: ['provider-models', channel.id, 'active', 'key-options', organizationID],
      enabled: enabled && !!organizationID && channels.isSuccess,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        loadKeyChannelModels(async page => {
          const response = await apiClient.get(`/admin/providers/${channel.id}/models`, {
            params: { page, page_size: 100, status: 'active' },
            signal,
          });
          return response.data.data;
        }),
    })),
  });
  const modelsLoading = channels.isFetching || modelQueries.some(query => query.isFetching);
  const modelsError = modelQueries.some(query => query.isError);
  const modelOptions = buildKeyModelOptions(
    modelChannels.map((channel, index) => ({ channel, models: modelQueries[index].data || [] })),
  );
  return { form, selectedModels, channels, modelQueries, modelsLoading, modelsError, modelOptions };
}

export function KeyAccessReadiness(props: {
  enabled: boolean;
  organizationID?: number;
  canManageOrganization?: boolean;
}) {
  const { t } = useI18n();
  const { channels, modelQueries, modelOptions, selectedModels, modelsLoading } =
    useKeyAccessOptions(props);
  const readiness = useQuery({
    queryKey: ['key-organization-readiness', props.organizationID],
    enabled: props.enabled && !!props.organizationID,
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(
        `/admin/organizations/${props.organizationID}/setup-status`,
        { signal, skipGlobalErrorMessage: true },
      );
      const id = response.data?.data?.provider_id;
      if (!Number.isSafeInteger(id) || id < 0) throw new Error('Incomplete organization readiness');
      return { providerID: id as number };
    },
  });
  const hasCallableModel = modelOptions.some(
    option => !selectedModels?.length || selectedModels.includes(option.value),
  );
  if (props.enabled && readiness.isError)
    return <LoadError retry={() => void readiness.refetch()} />;
  if (
    !props.enabled ||
    !props.organizationID ||
    modelsLoading ||
    !channels.isSuccess ||
    !modelQueries.every(query => query.isSuccess) ||
    !readiness.isSuccess ||
    (hasCallableModel && readiness.data.providerID > 0)
  )
    return null;
  return (
    <Alert
      type="warning"
      showIcon
      message={t('qa.no_access')}
      style={{ marginBottom: 16 }}
      description={
        <Typography.Link
          href={
            props.canManageOrganization
              ? firstKeyPath('/admin/organizations', props.organizationID, 'edit-organization')
              : '/admin/organizations'
          }
          target="_blank"
          rel="noopener noreferrer"
        >
          {t('qa.configure_access')}
        </Typography.Link>
      }
    />
  );
}

export function KeyAccessFields(props: { enabled: boolean; organizationID?: number }) {
  const { t } = useI18n();
  const { organizationID } = props;
  const { form, selectedModels, channels, modelQueries, modelsLoading, modelsError, modelOptions } =
    useKeyAccessOptions(props);
  const setModels = (models: string[]) =>
    form.setFields([{ name: 'allowed_models', value: models, touched: true, errors: [] }]);
  return (
    <>
      {channels.isError && <LoadError retry={() => void channels.refetch()} />}
      <Form.Item
        name="allowed_provider_ids"
        label={t('keys.allowed_channels')}
        extra={t('keys.access_policy_help')}
      >
        <Select
          mode="multiple"
          maxTagCount="responsive"
          allowClear
          showSearch
          optionFilterProp="label"
          disabled={!organizationID || channels.isError}
          loading={channels.isFetching}
          placeholder={t('keys.inherit_channels')}
          options={(channels.data || []).map(channel => ({
            label: channel.name,
            value: channel.id,
          }))}
        />
      </Form.Item>
      {modelsError && (
        <LoadError
          retry={() => {
            void Promise.all(
              modelQueries.filter(query => query.isError).map(query => query.refetch()),
            );
          }}
        />
      )}
      <div className="key-model-field">
        <div
          className="key-model-actions"
          role="group"
          aria-label={t('keys.model_selection_actions')}
        >
          <Button
            type="link"
            size="small"
            disabled={
              !organizationID ||
              channels.isError ||
              modelsLoading ||
              modelsError ||
              !modelOptions.length
            }
            onClick={() =>
              setModels(
                Array.from(
                  new Set([...(selectedModels || []), ...modelOptions.map(option => option.value)]),
                ),
              )
            }
          >
            {t('keys.select_all_models')}
          </Button>
          <Button
            type="link"
            size="small"
            disabled={!selectedModels?.length}
            onClick={() => setModels([])}
          >
            {t('keys.clear_models')}
          </Button>
        </div>
        <Form.Item
          name="allowed_models"
          label={t('keys.allowed_models')}
          extra={t('keys.model_policy_help')}
        >
          <Select
            mode="tags"
            maxTagCount="responsive"
            allowClear
            showSearch
            optionFilterProp="label"
            optionLabelProp="value"
            disabled={!organizationID || channels.isError}
            loading={modelsLoading}
            options={modelOptions}
            notFoundContent={
              modelsLoading ? (
                <Spin size="small" />
              ) : modelsError ? (
                t('ux.load_error')
              ) : (
                t('keys.no_channel_models')
              )
            }
            placeholder={t('keys.inherit_models')}
          />
        </Form.Item>
      </div>
    </>
  );
}
