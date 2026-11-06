import React from 'react';
import { Collapse, Space, Typography } from 'antd';
import { useGatewayConfig } from '../../config/gateway';
import { useI18n } from '../../i18n';
import { KeySecretActions, type KeySecretInfo } from '../keys/KeySecretActions';

export const ConnectionSettings: React.FC<{ keyInfo?: KeySecretInfo }> = ({ keyInfo }) => {
  const { t } = useI18n();
  const gateway = useGatewayConfig();
  const example = `export BOS_GATEWAY_URL="${gateway.baseUrl}"\nexport BOS_GATEWAY_KEY="YOUR_GATEWAY_KEY"`;
  return (
    <Space direction="vertical" size={20} style={{ width: '100%' }}>
      <Typography.Title level={5} style={{ margin: 0 }}>
        {t('ux.connection')}
      </Typography.Title>
      <div>
        <Typography.Text type="secondary">Base URL</Typography.Text>
        <div style={{ marginTop: 8 }}>
          {gateway.baseUrl ? (
            <Typography.Text code copyable>
              {gateway.baseUrl}
            </Typography.Text>
          ) : (
            <Typography.Text type="secondary">
              {t('gateway_settings.not_configured')}
            </Typography.Text>
          )}
        </div>
      </div>
      {keyInfo && (
        <div>
          <Typography.Text type="secondary">{t('keys.col_key')}</Typography.Text>
          <div style={{ marginTop: 8 }}>
            <KeySecretActions key={keyInfo.id} keyInfo={keyInfo} />
          </div>
        </div>
      )}
      <Typography.Text type="secondary">{t('ux.access_note')}</Typography.Text>
      {gateway.endpoint && (
        <Collapse
          items={[
            {
              key: 'example',
              label: t('keys.code_example'),
              children: (
                <Typography.Paragraph copyable={{ text: example }} style={{ margin: 0 }}>
                  <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', margin: 0 }}>
                    {example}
                  </pre>
                </Typography.Paragraph>
              ),
            },
          ]}
        />
      )}
      <a href="/docs/quickstart" target="_blank" rel="noopener noreferrer">
        {t('ux.help')} ↗
      </a>
    </Space>
  );
};
