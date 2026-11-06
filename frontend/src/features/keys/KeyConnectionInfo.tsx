import { GatewayReadinessNotice } from '../onboarding/GatewayReadinessNotice';
import { CopyOutlined } from '@ant-design/icons';
import { Button, Descriptions, Typography, message } from 'antd';
import { useGatewayConfig } from '../../config/gateway';
import { useI18n } from '../../i18n';
import { copyText } from '../common/copyText';
import './key-connection-info.css';

export function KeyCopyAllButton({ apiKey }: { apiKey: string }) {
  const { t } = useI18n();
  const gateway = useGatewayConfig();
  const loginUrl = new URL('/login', window.location.origin).href;
  const copyAll = async () => {
    if (!apiKey) return;
    const text = [
      `Key: ${apiKey}`,
      `${t('keys.copy_api_url_label')} ${gateway.baseUrl || t('gateway_settings.not_configured')}`,
      '',
      `${t('keys.copy_usage_url_label')} ${loginUrl}`,
      '',
      t('keys.copy_confidential_note'),
    ].join('\n');
    if (await copyText(text)) message.success(t('common.copied'));
    else message.warning(t('common.copy_failed'));
  };

  return (
    <Button icon={<CopyOutlined />} onClick={copyAll} disabled={!apiKey}>
      {t('keys.copy_all')}
    </Button>
  );
}

export function KeyConnectionInfo({
  apiKey,
  canConfigureAddress = false,
}: {
  apiKey: string;
  canConfigureAddress?: boolean;
}) {
  const { t } = useI18n();
  const gateway = useGatewayConfig();
  const loginUrl = new URL('/login', window.location.origin).href;

  return (
    <div className="key-connection-info">
      <GatewayReadinessNotice canConfigure={canConfigureAddress} />
      <Typography.Paragraph className="key-connection-secret" copyable={{ text: apiKey }}>
        <span className="key-connection-secret-value">{apiKey}</span>
      </Typography.Paragraph>
      <Descriptions
        column={1}
        layout="vertical"
        size="small"
        items={[
          {
            key: 'api',
            label: t('keys.api_url_label'),
            children: gateway.baseUrl ? (
              <Typography.Paragraph copyable={{ text: gateway.baseUrl }} code>
                {gateway.baseUrl}
              </Typography.Paragraph>
            ) : (
              <Typography.Text type="secondary">
                {t('gateway_settings.not_configured')}
              </Typography.Text>
            ),
          },
          {
            key: 'login',
            label: t('keys.login_url_label'),
            children: (
              <Typography.Paragraph copyable={{ text: loginUrl }}>
                <Typography.Link href={loginUrl} target="_blank" rel="noopener noreferrer">
                  {loginUrl}
                </Typography.Link>
              </Typography.Paragraph>
            ),
          },
        ]}
      />
    </div>
  );
}
