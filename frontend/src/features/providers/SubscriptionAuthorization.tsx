import React, { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Space, Typography, message } from 'antd';
import { CopyOutlined, LinkOutlined } from '@ant-design/icons';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { copyText } from '../common/copyText';

export interface SubscriptionAuthorizationValue {
  session_id: string;
  auth_url: string;
  state: string;
  expires_at: string;
  callback: string;
  project_id?: string;
}

export const SUBSCRIPTION_DEFAULTS: Record<string, { base_url: string; api_format: string }> = {
  openai: { base_url: 'https://chatgpt.com/backend-api/codex', api_format: 'openai_responses' },
  anthropic: { base_url: 'https://api.anthropic.com', api_format: 'anthropic_messages' },
  gemini: { base_url: 'https://cloudcode-pa.googleapis.com', api_format: 'gemini' },
  antigravity: { base_url: 'https://cloudcode-pa.googleapis.com', api_format: 'gemini' },
};

const PLATFORM_NAMES: Record<string, string> = {
  openai: 'ChatGPT / Codex',
  anthropic: 'Claude',
  gemini: 'Gemini',
  antigravity: 'Antigravity',
};

export const SubscriptionAuthorization: React.FC<{
  platform: string;
  connected: boolean;
  value?: SubscriptionAuthorizationValue;
  onChange?: (value: SubscriptionAuthorizationValue) => void;
}> = ({ platform, connected, value, onChange }) => {
  const { t } = useI18n();
  const platformName = PLATFORM_NAMES[platform] || platform;
  const loopbackCallback =
    platform === 'openai'
      ? 'localhost:1455/auth/callback'
      : platform === 'antigravity'
        ? 'localhost:8085/callback'
        : undefined;
  const [busy, setBusy] = useState(false);
  const request = useRef<AbortController>();
  useEffect(() => () => request.current?.abort(), []);
  const start = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    let signInWindow: Window | null = null;
    const closePendingWindow = () => {
      signInWindow?.close();
      signInWindow = null;
    };
    // Reserve the tab during the click so an asynchronous response is not blocked as a popup.
    try {
      signInWindow = window.open('about:blank', '_blank');
      if (signInWindow) signInWindow.opener = null;
    } catch {
      closePendingWindow();
    }
    controller.signal.addEventListener('abort', closePendingWindow, { once: true });
    setBusy(true);
    try {
      const response = await apiClient.post(`/admin/providers/oauth/${platform}/start`, undefined, {
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      onChange?.({ ...response.data.data, callback: '', project_id: value?.project_id || '' });
      try {
        if (!signInWindow || signInWindow.closed) throw new Error('Sign-in tab unavailable');
        signInWindow.location.replace(response.data.data.auth_url);
        signInWindow = null;
      } catch {
        closePendingWindow();
        message.warning(
          t(
            'providers.subscription_popup_blocked',
            'The sign-in page could not open automatically. Click “Open official sign-in page” below, or copy the authorization link.',
          ),
        );
      }
    } catch {
      closePendingWindow();
      // The API client displays the sanitized server error.
    } finally {
      controller.signal.removeEventListener('abort', closePendingWindow);
      if (!controller.signal.aborted) setBusy(false);
    }
  };

  const copyLink = async () => {
    if (!value?.auth_url) return;
    if (await copyText(value.auth_url)) {
      message.success(t('providers.subscription_copied', 'Authorization link copied.'));
    } else {
      message.warning(
        t('providers.subscription_copy_manual', 'Select and copy the authorization link above.'),
      );
    }
  };

  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Alert
        type="info"
        showIcon
        message={t(
          'providers.subscription_title',
          'Connect an official {platform} account',
        ).replace('{platform}', platformName)}
        description={t(
          'providers.subscription_help',
          'Start sign-in to open the official sign-in page in a new tab. You can also copy the authorization link for the account holder. After sign-in, paste the complete callback URL or authorization code below. No API key is needed.',
        )}
      />
      <Space wrap>
        <Button type="primary" icon={<LinkOutlined />} loading={busy} onClick={start}>
          {connected || value
            ? t('providers.subscription_restart', 'Restart official account sign-in')
            : t('providers.subscription_start', 'Start official account sign-in')}
        </Button>
      </Space>
      {value?.session_id && (
        <>
          {loopbackCallback && (
            <Alert
              type="warning"
              showIcon
              message={t(
                'providers.subscription_loopback_title',
                'A localhost connection error after sign-in? Copy the address to continue.',
              )}
              description={t(
                'providers.subscription_loopback_help',
                '{platform} redirects to {callback}. If the browser shows ERR_CONNECTION_REFUSED or cannot reach the page, copy the entire address, including code and state. Paste it below and choose Save Channel at the bottom of this form. Models can be added after saving. Keep this authorization session and form open while doing so.',
              )
                .replace('{platform}', platformName)
                .replace('{callback}', loopbackCallback)}
            />
          )}
          <Typography.Text strong>
            {t(
              'providers.subscription_link_label',
              '1. Sign in to {platform} or share the authorization link',
            ).replace('{platform}', platformName)}
          </Typography.Text>
          <Input.TextArea
            readOnly
            autoSize={{ minRows: 2, maxRows: 4 }}
            aria-label={t('providers.subscription_link', 'Official authorization link')}
            value={value.auth_url}
          />
          <Space wrap>
            <Button icon={<CopyOutlined />} onClick={copyLink}>
              {t('providers.subscription_copy', 'Copy authorization link')}
            </Button>
            <Button href={value.auth_url} target="_blank" rel="noopener noreferrer">
              {t('providers.subscription_open', 'Open official sign-in page')}
            </Button>
          </Space>
          <Typography.Text type="secondary">
            {t(
              'providers.subscription_expiry',
              'This authorization link expires after 10 minutes. Keep this form open while the account holder signs in.',
            )}
          </Typography.Text>
          <Typography.Text strong>
            {t(
              'providers.subscription_result',
              '2. Paste the authorization result returned by the account holder',
            )}
          </Typography.Text>
          <Typography.Text type="secondary">
            {t(
              'providers.subscription_callback_help',
              'Paste the complete final callback URL or displayed code, then choose Save Channel at the bottom of this form. If a localhost page fails to open, copy its full address anyway. Models may be left empty and configured after saving.',
            )}
          </Typography.Text>
          <Input.TextArea
            rows={3}
            autoComplete="off"
            aria-label={t('providers.subscription_callback', 'Callback URL or authorization code')}
            placeholder={t('providers.subscription_callback', 'Callback URL or authorization code')}
            value={value.callback}
            onChange={event => onChange?.({ ...value, callback: event.target.value })}
          />
          {(platform === 'gemini' || platform === 'antigravity') && (
            <Input
              autoComplete="off"
              aria-label={t('providers.subscription_project', 'Google Cloud Project ID (optional)')}
              placeholder={t(
                'providers.subscription_project',
                'Google Cloud Project ID (optional)',
              )}
              value={value.project_id}
              onChange={event => onChange?.({ ...value, project_id: event.target.value })}
            />
          )}
        </>
      )}
    </Space>
  );
};
