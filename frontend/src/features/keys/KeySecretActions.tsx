import React, { useState } from 'react';
import { Alert, Button, Form, Input, Modal, Select, Space, Typography, message } from 'antd';
import { TableAction } from '../common/TableAction';
import { EyeOutlined, ExportOutlined } from '@ant-design/icons';
import { apiClient } from '../../api/client';
import { useGatewayConfig } from '../../config/gateway';
import { useI18n } from '../../i18n';
import { buildClientImportLink, type CodingClientApp, type KeyImportTarget } from './clientImport';
import { KeyConnectionInfo, KeyCopyAllButton } from './KeyConnectionInfo';

export interface KeySecretInfo {
  id: number;
  name: string;
  maskedKey?: string;
}

async function readSecret(id: number, code: string): Promise<string> {
  const response = await apiClient.post(`/admin/keys/${id}/reveal`, { code });
  const secret = response.data?.data?.key;
  if (typeof secret !== 'string' || !secret) throw new Error('API key is unavailable');
  return secret;
}

export const KeyClientImportModal: React.FC<{
  target: KeyImportTarget;
  keyInfo: KeySecretInfo | null;
  onClose: () => void;
}> = ({ target, keyInfo, onClose }) => {
  const { t } = useI18n();
  const gateway = useGatewayConfig();
  const [app, setApp] = useState<CodingClientApp>('claude');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const messages = target === 'vary-ai' ? 'vary_ai' : 'cc_switch';
  const clear = () => {
    setApp('claude');
    setCode('');
    onClose();
  };
  const importKey = async () => {
    if (!keyInfo || busy) return;
    if (!gateway.endpoint) {
      message.warning(t('gateway_settings.not_configured'));
      return;
    }
    if (!/^\d{6}$/.test(code)) {
      message.warning(t('keys.err_totp_required'));
      return;
    }
    setBusy(true);
    try {
      const secret = await readSecret(keyInfo.id, code);
      const url = buildClientImportLink(target, {
        app,
        name: keyInfo.name,
        endpoint: gateway.endpoint,
        apiKey: secret,
      });
      window.location.assign(url);
      message.info(t(`keys.${messages}_opened`));
      clear();
    } catch (error: any) {
      if (!error?.response) message.error(t(`keys.${messages}_failed`));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={t(`keys.${messages}_import`)}
      open={!!keyInfo}
      onCancel={() => {
        if (!busy) clear();
      }}
      onOk={importKey}
      okText={t('keys.cc_switch_open')}
      cancelText={t('common.cancel')}
      confirmLoading={busy}
      okButtonProps={{ disabled: !gateway.endpoint }}
      maskClosable={!busy}
      closable={!busy}
      destroyOnHidden
    >
      {!gateway.endpoint && (
        <Alert type="warning" showIcon message={t('gateway_settings.not_configured')} />
      )}
      <Form layout="vertical" style={{ marginTop: 20 }} disabled={busy || !gateway.endpoint}>
        <Form.Item label={t('keys.cc_switch_client')}>
          <Select
            value={app}
            onChange={setApp}
            options={[
              { value: 'claude', label: 'Claude Code' },
              { value: 'codex', label: 'Codex' },
            ]}
          />
        </Form.Item>
        <Form.Item label={t('keys.modal_reveal_title')}>
          <Input
            value={code}
            onChange={event => setCode(event.target.value.replace(/\D/g, ''))}
            placeholder={t('keys.reveal_code_placeholder')}
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            onPressEnter={importKey}
          />
        </Form.Item>
        <Typography.Paragraph type="secondary">{t(`keys.${messages}_note`)}</Typography.Paragraph>
        <Typography.Text type="secondary">
          {keyInfo?.name} · {app === 'codex' ? gateway.baseUrl : gateway.endpoint}
        </Typography.Text>
      </Form>
    </Modal>
  );
};

export const KeySecretActions: React.FC<{
  keyInfo: KeySecretInfo;
  compact?: boolean;
  showImport?: boolean;
}> = ({ keyInfo, compact = false, showImport = true }) => {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [viewing, setViewing] = useState(false);
  const [code, setCode] = useState('');
  const [secret, setSecret] = useState<string | null>(null);
  const [importing, setImporting] = useState<{ target: KeyImportTarget; open: boolean }>({
    target: 'cc-switch',
    open: false,
  });
  const close = () => {
    if (!busy) {
      setViewing(false);
      setCode('');
      setSecret(null);
    }
  };
  const reveal = async () => {
    if (busy) return;
    if (!/^\d{6}$/.test(code)) {
      message.warning(t('keys.err_totp_required'));
      return;
    }
    setBusy(true);
    try {
      setSecret(await readSecret(keyInfo.id, code));
      setCode('');
    } catch (error: any) {
      if (!error?.response) message.error(t('keys.reveal_failed'));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Space size={compact ? 4 : 8} wrap={!compact}>
        {keyInfo.maskedKey && (
          <Typography.Text code style={{ whiteSpace: 'nowrap' }}>
            {keyInfo.maskedKey}
          </Typography.Text>
        )}
        {compact ? (
          <TableAction
            label={t('keys.btn_reveal')}
            icon={<EyeOutlined />}
            onClick={() => setViewing(true)}
          />
        ) : (
          <Button icon={<EyeOutlined />} onClick={() => setViewing(true)}>
            {t('keys.btn_reveal')}
          </Button>
        )}
        {showImport && (
          <>
            <Button
              icon={<ExportOutlined />}
              onClick={() => setImporting({ target: 'cc-switch', open: true })}
            >
              {t('keys.cc_switch_import')}
            </Button>
            <Button
              icon={<ExportOutlined />}
              onClick={() => setImporting({ target: 'vary-ai', open: true })}
            >
              {t('keys.vary_ai_import')}
            </Button>
          </>
        )}
      </Space>
      <Modal
        title={secret === null ? t('keys.modal_reveal_title') : keyInfo.name}
        open={viewing}
        onCancel={close}
        onOk={secret === null ? reveal : close}
        okText={secret === null ? t('keys.btn_verify_reveal') : t('common.close')}
        footer={
          secret === null
            ? undefined
            : (_, { OkBtn }) => (
                <>
                  <KeyCopyAllButton apiKey={secret} />
                  <OkBtn />
                </>
              )
        }
        confirmLoading={busy}
        maskClosable={!busy}
        closable={!busy}
        destroyOnHidden
      >
        {secret === null ? (
          <>
            <Typography.Paragraph type="secondary">
              {t('keys.modal_reveal_desc')}
            </Typography.Paragraph>
            <Input
              value={code}
              onChange={event => setCode(event.target.value.replace(/\D/g, ''))}
              placeholder={t('keys.reveal_code_placeholder')}
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              autoFocus
              onPressEnter={reveal}
            />
          </>
        ) : (
          <>
            <Typography.Paragraph type="secondary">{t('keys.revealed_desc')}</Typography.Paragraph>
            <KeyConnectionInfo apiKey={secret} />
          </>
        )}
      </Modal>
      {showImport && (
        <KeyClientImportModal
          target={importing.target}
          keyInfo={importing.open ? keyInfo : null}
          onClose={() => setImporting(current => ({ ...current, open: false }))}
        />
      )}
    </>
  );
};
