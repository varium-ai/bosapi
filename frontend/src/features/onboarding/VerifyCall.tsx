import { useEffect, useRef, useState } from 'react';
import { Alert, Form, Input, Modal, Select } from 'antd';
import { useI18n } from '../../i18n';
import { useGatewayConfig } from '../../config/gateway';

export function VerifyCall({
  open,
  onClose,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
}) {
  const { language } = useI18n();
  const gateway = useGatewayConfig();
  const zh = language === 'zh-CN';
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const controller = useRef<AbortController>();
  useEffect(() => () => controller.current?.abort(), []);
  const close = () => {
    if (running.current) return;
    form.resetFields();
    onClose();
  };
  const test = async (values: { key: string; model: string; protocol: string }) => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    controller.current = new AbortController();
    const timeout = window.setTimeout(() => controller.current?.abort(), 30000);
    const protocol = values.protocol;
    const endpoint =
      protocol === 'responses'
        ? '/responses'
        : protocol === 'messages'
          ? '/messages'
          : '/chat/completions';
    const payload =
      protocol === 'responses'
        ? { model: values.model.trim(), input: 'Reply with OK.', max_output_tokens: 32 }
        : {
            model: values.model.trim(),
            messages: [{ role: 'user', content: 'Reply with OK.' }],
            max_tokens: 32,
          };
    try {
      const response = await fetch(`${gateway.baseUrl || '/v1'}${endpoint}`, {
        method: 'POST',
        credentials: 'omit',
        signal: controller.current.signal,
        headers: {
          'Content-Type': 'application/json',
          ...(protocol === 'messages'
            ? { 'x-api-key': values.key.trim(), 'anthropic-version': '2023-06-01' }
            : { Authorization: `Bearer ${values.key.trim()}` }),
        },
        body: JSON.stringify(payload),
      });
      const body = await response.json().catch(() => null);
      if (!response.ok || body?.error)
        throw {
          response: {
            status: response.status,
            data: {
              error: { code: body?.error?.code },
              request_id: response.headers.get('x-request-id'),
            },
          },
        };
      const valid =
        protocol === 'responses'
          ? body?.status === 'completed' && body?.output?.length
          : protocol === 'messages'
            ? body?.content?.some((item: any) => item.type === 'text' && item.text)
            : body?.choices?.some((item: any) => item.message?.content);
      if (!valid)
        throw { response: { status: response.status, data: { error: { code: 'unknown_error' } } } };
      form.resetFields();
      onSuccess();
      onClose();
    } catch (error: any) {
      window.dispatchEvent(
        new CustomEvent('bos:configuration-error', {
          detail:
            error?.name === 'AbortError'
              ? { operation: 'verify-call', code: 'ECONNABORTED', config: { method: 'get' } }
              : { ...error, operation: 'verify-call' },
        }),
      );
    } finally {
      window.clearTimeout(timeout);
      running.current = false;
      setBusy(false);
    }
  };
  return (
    <Modal
      open={open}
      title={zh ? '验证首次调用' : 'Verify your first call'}
      onCancel={close}
      onOk={() => form.submit()}
      okText={zh ? '发送一次测试请求' : 'Send one test request'}
      confirmLoading={busy}
      cancelButtonProps={{ disabled: busy }}
      closable={!busy}
      maskClosable={!busy}
      destroyOnHidden
    >
      <Alert
        type="warning"
        showIcon
        message={
          zh
            ? '主动测试会发送实际请求，可能产生上游费用。不会自动重试。'
            : 'This sends a real request and may incur upstream charges. It will not retry automatically.'
        }
        style={{ marginBottom: 16 }}
      />
      <Form
        form={form}
        layout="vertical"
        onFinish={test}
        disabled={busy}
        initialValues={{ protocol: 'chat' }}
      >
        <Form.Item
          name="key"
          label="API Key"
          rules={[{ required: true }]}
          extra={
            zh
              ? '输入当前组织的完整网关 Key。仅用于此次请求，不写入浏览器存储。'
              : 'Use the full gateway Key for the selected organization. Used only for this request; never persisted in the browser.'
          }
        >
          <Input.Password autoComplete="off" />
        </Form.Item>
        <Form.Item
          name="model"
          label={zh ? '授权模型 ID' : 'Allowed model ID'}
          rules={[{ required: true, whitespace: true }]}
        >
          <Input />
        </Form.Item>
        <Form.Item name="protocol" label={zh ? '请求协议' : 'Protocol'}>
          <Select
            options={[
              { value: 'chat', label: 'Chat Completions' },
              { value: 'responses', label: 'Responses' },
              { value: 'messages', label: 'Messages' },
            ]}
          />
        </Form.Item>
      </Form>
      <a href="/docs/quickstart#first-request" target="_blank" rel="noreferrer">
        {zh
          ? '也可使用文档示例调用，再返回刷新进度'
          : 'Or run a documentation example, then return and refresh progress'}
      </a>
    </Modal>
  );
}
