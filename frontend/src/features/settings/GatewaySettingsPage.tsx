import React from 'react';
import { Button, Card, Form, Input, message, Typography } from 'antd';
import { SaveOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { getGatewayConfig } from '../../config/gateway';
import { normalizeGatewayEndpoint } from '../../config/gatewayEndpoint';
import type { GatewayEndpointSettings } from '../../config/gatewayEndpoint';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';

const settingsPath = '/admin/settings/gateway-endpoints';
const settingsQueryKey = ['gateway-endpoint-settings'];

export const GatewaySettingsPage: React.FC = () => {
  const { t } = useI18n();
  const [form] = Form.useForm<GatewayEndpointSettings>();
  const hasUnsavedChanges = React.useRef(false);
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: settingsQueryKey,
    queryFn: async () => {
      const response = await apiClient.get(settingsPath, { skipGlobalErrorMessage: true });
      return response.data.data as GatewayEndpointSettings;
    },
  });

  React.useEffect(() => {
    if (settings.data && !hasUnsavedChanges.current) form.setFieldsValue(settings.data);
  }, [settings.data, form]);

  const save = useMutation({
    mutationFn: async (values: GatewayEndpointSettings) => {
      const response = await apiClient.put(
        settingsPath,
        {
          endpoint: normalizeGatewayEndpoint(values.endpoint),
        },
        { skipGlobalErrorMessage: true },
      );
      return response.data.data as GatewayEndpointSettings;
    },
    onSuccess: async values => {
      await Promise.all([
        queryClient.cancelQueries({ queryKey: settingsQueryKey }),
        queryClient.cancelQueries({ queryKey: ['gateway-config'] }),
      ]);
      hasUnsavedChanges.current = false;
      form.setFieldsValue(values);
      queryClient.setQueryData(settingsQueryKey, values);
      queryClient.setQueryData(['gateway-config'], getGatewayConfig(values.endpoint));
      message.success(t('gateway_settings.saved'));
    },
    onError: (error: any) => message.error(error?.userMessage || t('gateway_settings.save_error')),
  });

  const endpointRules = [
    {
      required: true,
      validator: (_: unknown, value: unknown) => {
        try {
          if (typeof value !== 'string') throw new Error();
          normalizeGatewayEndpoint(value);
          return Promise.resolve();
        } catch {
          return Promise.reject(new Error(t('gateway_settings.invalid_url')));
        }
      },
    },
  ];

  return (
    <Card title={t('gateway_settings.title')} loading={settings.isLoading}>
      <Typography.Paragraph type="secondary">
        {t('gateway_settings.description')}
      </Typography.Paragraph>
      {settings.isError && (
        <LoadError
          retry={() => {
            if (!settings.isFetching) void settings.refetch();
          }}
        />
      )}
      <Form
        form={form}
        layout="vertical"
        style={{ maxWidth: 640 }}
        disabled={!settings.data || save.isPending}
        onValuesChange={() => {
          hasUnsavedChanges.current = true;
        }}
        onFinish={values => {
          if (settings.data && !save.isPending) save.mutate(values);
        }}
      >
        <Form.Item
          name="endpoint"
          label={t('gateway_settings.endpoint')}
          rules={endpointRules}
          extra={t('gateway_settings.url_help')}
        >
          <Input placeholder={t('gateway_settings.endpoint_placeholder')} autoComplete="off" />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={save.isPending}>
            {t('common.save_changes')}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
