import React from 'react';
import {
  Alert,
  Button,
  Card,
  Collapse,
  Form,
  Input,
  InputNumber,
  message,
  Radio,
  Space,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { SaveOutlined, InfoCircleOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError } from '../common/ConsoleUX';

const { Text } = Typography;

interface NotificationSettings {
  provider: 'lark' | 'email' | 'webhook';
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  from_email: string;
  from_name: string;
  alert_emails: string[];
  has_lark_webhook: boolean;
  masked_lark_webhook?: string;
  has_webhook_url: boolean;
  masked_webhook_url?: string;
  has_smtp_password: boolean;
}

export const MessageNotificationConfigPage: React.FC = () => {
  const { t } = useI18n();
  const [form] = Form.useForm();
  const [editingWebhook, setEditingWebhook] = React.useState<'lark' | 'webhook' | null>(null);
  const hasUnsavedChanges = React.useRef(false);
  const [dirty, setDirty] = React.useState(false);
  const provider = Form.useWatch('provider', form) || 'lark';
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ['message-notification-settings'],
    queryFn: async () => {
      const response = await apiClient.get('/admin/message-notification-settings');
      return response.data?.data as NotificationSettings;
    },
  });
  const isGenericWebhook = provider === 'webhook';
  const webhookField = isGenericWebhook ? 'webhook_url' : 'lark_webhook';
  const hasWebhook = isGenericWebhook
    ? settings.data?.has_webhook_url
    : settings.data?.has_lark_webhook;
  const maskedWebhook = isGenericWebhook
    ? settings.data?.masked_webhook_url
    : settings.data?.masked_lark_webhook;
  const webhookLabel = isGenericWebhook
    ? t('message_config.webhook_url', 'Webhook URL')
    : t('message_config.lark_url', 'Lark bot URL');
  const webhookPlaceholder = isGenericWebhook
    ? 'https://example.com/webhook'
    : 'https://open.feishu.cn/open-apis/bot/v2/hook/...';
  const webhookNote = isGenericWebhook
    ? t(
        'message_config.webhook_note',
        'Administrator OTP codes are sent to this receiver as JSON. Keep its URL private.',
      )
    : t(
        'message_config.lark_note',
        'The bot receives the administrator OTP message. Keep its URL private.',
      );

  React.useEffect(() => {
    if (!settings.data || hasUnsavedChanges.current) return;
    setEditingWebhook(null);
    form.setFieldsValue({
      ...settings.data,
      smtp_port: settings.data.smtp_port > 0 ? settings.data.smtp_port : 587,
      alert_emails: (settings.data.alert_emails || []).join('\n'),
    });
  }, [settings.data, form]);

  const save = useMutation({
    mutationFn: async (values: any) => {
      const isEmail = values.provider === 'email';
      const alertEmails = isEmail
        ? String(values.alert_emails || '')
            .split(/[\n,;]+/)
            .map(email => email.trim())
            .filter(Boolean)
        : [];
      await apiClient.put('/admin/message-notification-settings', {
        ...values,
        alert_emails: alertEmails,
      });
    },
    onSuccess: async () => {
      message.success(t('message_config.saved', 'Message delivery settings saved'));
      hasUnsavedChanges.current = false;
      setDirty(false);
      smtpTest.reset();
      setEditingWebhook(null);
      form.setFieldsValue({ lark_webhook: '', webhook_url: '', smtp_password: '' });
      await queryClient.invalidateQueries({ queryKey: ['message-notification-settings'] });
    },
    onError: (error: any) =>
      message.error(
        error?.userMessage || t('message_config.save_error', 'Failed to save message settings'),
      ),
  });

  const smtpTest = useMutation({
    mutationFn: async () =>
      (
        await apiClient.post(
          '/admin/message-notification-settings/test',
          {},
          { skipGlobalErrorMessage: true },
        )
      ).data.data as { encrypted: boolean },
  });

  const descriptionText =
    provider === 'email'
      ? t(
          'message_config.description_email',
          'Choose how administrator OTP codes are delivered. OTP email is sent to the address entered in the administrator form. Alert emails use the recipient list below.',
        )
      : isGenericWebhook
        ? t(
            'message_config.description_webhook',
            'Deliver administrator OTP codes to a custom HTTPS receiver with JSON POST requests.',
          )
        : t(
            'message_config.description_lark',
            'Configure administrator OTP delivery via Lark bot. The bot receives OTP messages sent to administrators.',
          );

  return (
    <Card
      title={
        <Space align="center" size={8} style={{ maxWidth: '100%' }}>
          <span>{t('message_config.title', 'Message (OTP) Configuration')}</span>
          <Tooltip title={descriptionText}>
            <Text
              type="secondary"
              style={{
                fontSize: 12,
                fontWeight: 'normal',
                cursor: 'pointer',
                display: 'inline-flex',
                alignItems: 'center',
                gap: 4,
              }}
            >
              <InfoCircleOutlined style={{ color: '#8c8c8c' }} />
              <span
                style={{
                  maxWidth: 460,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {descriptionText}
              </span>
            </Text>
          </Tooltip>
        </Space>
      }
      loading={settings.isLoading}
    >
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
        initialValues={{ provider: 'lark', smtp_port: 587 }}
        disabled={!settings.data || save.isPending || smtpTest.isPending}
        scrollToFirstError={{ block: 'center', focus: true }}
        onValuesChange={changed => {
          if (changed.provider === 'email' && !form.getFieldValue('smtp_port'))
            form.setFieldValue('smtp_port', 587);
          hasUnsavedChanges.current = true;
          setDirty(true);
          smtpTest.reset();
        }}
        onFinish={values => {
          if (settings.data && !save.isPending) save.mutate(values);
        }}
      >
        <Form.Item
          name="provider"
          label={t('message_config.provider', 'OTP delivery method')}
          rules={[{ required: true }]}
        >
          <Radio.Group
            options={[
              { value: 'lark', label: t('message_config.lark', 'Lark bot') },
              { value: 'email', label: t('message_config.email', 'Email / SMTP') },
              { value: 'webhook', label: t('message_config.webhook', 'Generic Webhook') },
            ]}
          />
        </Form.Item>
        {provider !== 'email' ? (
          <>
            {hasWebhook && editingWebhook !== provider ? (
              <div style={{ marginBottom: 16 }}>
                <Text strong style={{ display: 'block', marginBottom: 6 }}>
                  {webhookLabel}
                </Text>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '10px 14px',
                    background: '#fafafa',
                    border: '1px solid #d9d9d9',
                    borderRadius: 6,
                    marginBottom: 8,
                    flexWrap: 'wrap',
                    gap: 8,
                  }}
                >
                  <Space size={10} style={{ minWidth: 0, flex: 1 }}>
                    <Tag color="success" bordered={false} style={{ margin: 0, fontWeight: 500 }}>
                      {t('message_config.configured_active', 'Configured & Active')}
                    </Tag>
                    <Text code style={{ fontSize: 13, wordBreak: 'break-all' }}>
                      {maskedWebhook || '****'}
                    </Text>
                  </Space>
                  <Button
                    size="small"
                    type="primary"
                    ghost
                    onClick={() => {
                      hasUnsavedChanges.current = true;
                      setDirty(true);
                      smtpTest.reset();
                      setEditingWebhook(isGenericWebhook ? 'webhook' : 'lark');
                      form.setFieldValue(webhookField, '');
                    }}
                  >
                    {t('message_config.btn_edit_webhook', 'Change / Replace URL')}
                  </Button>
                </div>
                <Text type="secondary">{webhookNote}</Text>
              </div>
            ) : (
              <>
                <Form.Item
                  key={webhookField}
                  name={webhookField}
                  label={
                    <Space>
                      <span>{webhookLabel}</span>
                      {hasWebhook && (
                        <Button
                          type="link"
                          size="small"
                          style={{ padding: 0, height: 'auto', fontSize: 12 }}
                          onClick={() => {
                            setEditingWebhook(null);
                            form.setFieldValue(webhookField, '');
                          }}
                        >
                          {t('message_config.cancel_edit', 'Cancel (Keep current)')}
                        </Button>
                      )}
                    </Space>
                  }
                  rules={[
                    {
                      required: !hasWebhook,
                      type: 'url',
                      message: isGenericWebhook
                        ? t(
                            'message_config.webhook_url_invalid',
                            'Enter a public HTTPS Webhook URL',
                          )
                        : t('message_config.lark_url_invalid', 'Enter a valid HTTPS bot URL'),
                    },
                    {
                      pattern: /^https:\/\//,
                      message: isGenericWebhook
                        ? t(
                            'message_config.webhook_url_invalid',
                            'Enter a public HTTPS Webhook URL',
                          )
                        : t('message_config.lark_url_invalid', 'Enter a valid HTTPS bot URL'),
                    },
                  ]}
                  extra={
                    hasWebhook
                      ? isGenericWebhook
                        ? t(
                            'message_config.new_generic_webhook_help',
                            'Enter a new receiver URL; saving will replace the current Webhook URL.',
                          )
                        : t(
                            'message_config.new_webhook_help',
                            'Enter a new Lark bot Webhook URL; saving will replace the existing configuration.',
                          )
                      : undefined
                  }
                >
                  <Input placeholder={webhookPlaceholder} autoComplete="new-password" />
                </Form.Item>
                <Text type="secondary">{webhookNote}</Text>
              </>
            )}
            {isGenericWebhook && (
              <Collapse
                ghost
                size="small"
                style={{ marginTop: 16 }}
                items={[
                  {
                    key: 'request-format',
                    label: t('message_config.webhook_payload', 'Request format and delivery'),
                    children: (
                      <>
                        <Typography.Paragraph type="secondary">
                          {t(
                            'message_config.webhook_format_help',
                            'POST with Content-Type: application/json and type=otp. Any HTTP 2xx response confirms delivery; no specific response body is required. Requests are not redirected.',
                          )}
                        </Typography.Paragraph>
                        <pre
                          style={{
                            margin: 0,
                            padding: 12,
                            borderRadius: 6,
                            background: '#fafafa',
                            whiteSpace: 'pre-wrap',
                            overflowWrap: 'anywhere',
                          }}
                        >
                          {JSON.stringify(
                            {
                              type: 'otp',
                              subject: 'BOS API verification code',
                              text: 'Your BOS API verification code is 123456. It expires in 5 minutes.',
                              email: 'admin@example.com',
                              code: '123456',
                              expires_in: 300,
                            },
                            null,
                            2,
                          )}
                        </pre>
                      </>
                    ),
                  },
                ]}
              />
            )}
          </>
        ) : (
          <>
            <Alert type="info" showIcon message={t('qa.smtp_tls')} style={{ marginBottom: 16 }} />
            <Space direction="vertical" style={{ marginBottom: 16, width: '100%' }}>
              <Button
                loading={smtpTest.isPending}
                disabled={dirty || save.isPending || settings.data?.provider !== 'email'}
                onClick={() => smtpTest.mutate()}
              >
                {t('qa.smtp_test')}
              </Button>
              <Text type="secondary">{t('qa.smtp_test_help')}</Text>
              {smtpTest.isError && (
                <Alert type="error" showIcon message={t('qa.smtp_test_failed')} />
              )}
              {smtpTest.isSuccess && (
                <Alert
                  type={smtpTest.data.encrypted ? 'success' : 'warning'}
                  showIcon
                  message={t(smtpTest.data.encrypted ? 'qa.smtp_test_ok' : 'qa.smtp_test_plain')}
                />
              )}
            </Space>
            <Space align="start" style={{ display: 'flex' }} size="large">
              <Form.Item
                name="smtp_host"
                label={t('message_config.smtp_host', 'SMTP host')}
                rules={[{ required: true }]}
                style={{ flex: 1 }}
              >
                <Input placeholder="smtp.example.com" />
              </Form.Item>
              <Form.Item
                name="smtp_port"
                label={t('message_config.smtp_port', 'SMTP port')}
                rules={[{ required: true }]}
              >
                <InputNumber min={1} max={65535} style={{ width: 130 }} />
              </Form.Item>
            </Space>
            <Space align="start" style={{ display: 'flex' }} size="large">
              <Form.Item
                name="smtp_username"
                label={t('message_config.smtp_username', 'SMTP username')}
                style={{ flex: 1 }}
              >
                <Input autoComplete="off" />
              </Form.Item>
              <Form.Item
                name="smtp_password"
                label={t('message_config.smtp_password', 'SMTP password')}
                style={{ flex: 1 }}
              >
                <Input.Password
                  placeholder={
                    settings.data?.has_smtp_password
                      ? t(
                          'message_config.secret_preserved',
                          'Configured; leave blank to keep current value',
                        )
                      : ''
                  }
                  autoComplete="new-password"
                />
              </Form.Item>
            </Space>
            <Space align="start" style={{ display: 'flex' }} size="large">
              <Form.Item
                name="from_email"
                label={t('message_config.from_email', 'Sender email')}
                rules={[{ required: true, type: 'email' }]}
                style={{ flex: 1 }}
              >
                <Input placeholder="notifications@example.com" />
              </Form.Item>
              <Form.Item
                name="from_name"
                label={t('message_config.from_name', 'Sender name')}
                style={{ flex: 1 }}
              >
                <Input />
              </Form.Item>
            </Space>
            <Form.Item
              name="alert_emails"
              label={t('message_config.alert_emails', 'Alert recipient email list')}
              extra={t(
                'message_config.alert_emails_help',
                'Enter one address per line, or separate addresses with commas.',
              )}
            >
              <Input.TextArea rows={4} placeholder={'ops@example.com\nadmin@example.com'} />
            </Form.Item>
          </>
        )}
        <Form.Item style={{ marginTop: 24, marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={save.isPending}>
            {t('common.save_changes', 'Save Changes')}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
