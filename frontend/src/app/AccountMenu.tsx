import React, { useState } from 'react';
import { Button, Dropdown, Form, Modal, Select, Tag, message } from 'antd';
import type { MenuProps } from 'antd';
import {
  DownOutlined,
  ExportOutlined,
  FileTextOutlined,
  KeyOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import { useI18n } from '../i18n';
import { TIMEZONE_OPTIONS, getTimezoneLabel } from '../i18n/formatters';
import type { SupportedLanguage, SupportedTimezone } from '../i18n/types';
import { LANGUAGE_OPTIONS } from '../i18n/languages';
import './account-menu.css';

interface AccountMenuProps {
  user: { email: string; role: string; totp_enabled?: boolean; totp_enabled_at?: string } | null;
  onMFA: () => void;
  onChangePassword: () => void;
  onLogout: () => Promise<void>;
}

export const AccountMenu: React.FC<AccountMenuProps> = ({
  user,
  onMFA,
  onChangePassword,
  onLogout,
}) => {
  const { t, language, timezone, timezoneBadge, setLanguage, setTimezone } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const [preferencesOpen, setPreferencesOpen] = useState(false);
  const [logoutOpen, setLogoutOpen] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [draftLanguage, setDraftLanguage] = useState<SupportedLanguage>(language);
  const [draftTimezone, setDraftTimezone] = useState<SupportedTimezone>(timezone);
  const canManageSecurity = Boolean(user && user.role !== 'key_viewer');
  const roleLabel =
    user?.role === 'key_viewer'
      ? t('account.key_viewer')
      : t(`nav.role_${user?.role}`, user?.role || t('nav.profile'));

  const openPreferences = () => {
    setDraftLanguage(language);
    setDraftTimezone(timezone);
    setPreferencesOpen(true);
  };

  const items: MenuProps['items'] = [
    {
      type: 'group',
      key: 'preferences-group',
      label: t('account.preferences'),
      children: [
        {
          key: 'preferences',
          icon: <SettingOutlined />,
          label: (
            <span className="account-menu-row">
              <span>{t('account.language_timezone')}</span>
              <span className="account-menu-hint">
                {t('account.language_short')} · {timezoneBadge}
              </span>
            </span>
          ),
          onClick: openPreferences,
        },
      ],
    },
    ...(canManageSecurity
      ? [
          {
            type: 'group' as const,
            key: 'security-group',
            label: t('account.security'),
            children: [
              {
                key: 'mfa',
                icon: <SafetyCertificateOutlined />,
                label: (
                  <span className="account-menu-row">
                    <span>{t('account.two_factor')}</span>
                    <span
                      className={`account-mfa-status ${user?.totp_enabled ? 'is-enabled' : ''}`}
                    >
                      {t(user?.totp_enabled ? 'account.enabled' : 'account.not_enabled')}
                    </span>
                  </span>
                ),
                onClick: onMFA,
              },
              {
                key: 'password',
                icon: <KeyOutlined />,
                label: t('nav.change_password'),
                onClick: onChangePassword,
              },
            ],
          },
        ]
      : []),
    {
      type: 'group',
      key: 'help-group',
      label: t('account.help'),
      children: [
        {
          key: 'documentation',
          icon: <FileTextOutlined />,
          label: (
            <a
              className="account-documentation-link"
              href="/docs/quickstart"
              target="_blank"
              rel="noopener noreferrer"
            >
              <span>{t('nav.documentation')}</span>
              <ExportOutlined className="account-menu-hint" aria-hidden="true" />
            </a>
          ),
        },
      ],
    },
    { type: 'divider' },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: t('nav.logout'),
      danger: true,
      onClick: () => setLogoutOpen(true),
    },
  ];

  return (
    <>
      <Dropdown
        autoFocus
        trigger={['click']}
        placement="bottomRight"
        open={menuOpen}
        onOpenChange={setMenuOpen}
        overlayClassName="account-dropdown"
        menu={{ items, onClick: () => setMenuOpen(false) }}
        popupRender={menu => (
          <div className="account-menu-panel">
            <div className="account-menu-profile">
              <span className="account-avatar account-avatar-large" aria-hidden="true">
                {user?.email?.slice(0, 2).toUpperCase() || 'B'}
              </span>
              <div className="account-profile-details">
                <div className="account-profile-email">{user?.email || t('nav.profile')}</div>
                <Tag className="account-role">{roleLabel}</Tag>
              </div>
            </div>
            {menu}
          </div>
        )}
      >
        <Button
          type="text"
          className={`account-trigger ${menuOpen ? 'is-open' : ''}`}
          aria-label={t('account.menu')}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
        >
          <span className="account-avatar" aria-hidden="true">
            {user?.email?.slice(0, 2).toUpperCase() || 'B'}
          </span>
          <span className="account-trigger-email">{user?.email || t('nav.profile')}</span>
          <DownOutlined className="account-trigger-chevron" />
        </Button>
      </Dropdown>

      <Modal
        title={t('account.preferences')}
        open={preferencesOpen}
        width={460}
        okText={t('common.save_changes')}
        cancelText={t('common.cancel')}
        onCancel={() => setPreferencesOpen(false)}
        onOk={() => {
          setLanguage(draftLanguage);
          setTimezone(draftTimezone);
          setPreferencesOpen(false);
        }}
      >
        <p className="account-modal-description">{t('account.preferences_description')}</p>
        <Form layout="vertical">
          <Form.Item label={t('nav.language')} htmlFor="account-language">
            <Select
              id="account-language"
              value={draftLanguage}
              onChange={setDraftLanguage}
              options={LANGUAGE_OPTIONS.map(option => ({
                value: option.value,
                label: t(option.labelKey),
              }))}
            />
          </Form.Item>
          <Form.Item
            label={t('nav.timezone')}
            htmlFor="account-timezone"
            extra={t('nav.timezone_desc')}
          >
            <Select
              id="account-timezone"
              value={draftTimezone}
              onChange={setDraftTimezone}
              options={TIMEZONE_OPTIONS.map(option => ({
                value: option.value,
                label: getTimezoneLabel(option, language),
              }))}
            />
          </Form.Item>
        </Form>
        <div className="account-preferences-note">
          <SettingOutlined />
          {t('account.browser_only')}
        </div>
      </Modal>

      <Modal
        title={t('account.logout_title')}
        open={logoutOpen}
        width={400}
        okText={t('nav.logout')}
        cancelText={t('common.cancel')}
        okButtonProps={{ danger: true }}
        confirmLoading={loggingOut}
        closable={!loggingOut}
        maskClosable={!loggingOut}
        keyboard={!loggingOut}
        cancelButtonProps={{ disabled: loggingOut }}
        onCancel={() => {
          if (!loggingOut) setLogoutOpen(false);
        }}
        onOk={async () => {
          setLoggingOut(true);
          try {
            await onLogout();
            setLogoutOpen(false);
          } catch {
            message.error(t('common.error'));
          } finally {
            setLoggingOut(false);
          }
        }}
      >
        <p className="account-modal-description">{t('account.logout_description')}</p>
      </Modal>
    </>
  );
};
