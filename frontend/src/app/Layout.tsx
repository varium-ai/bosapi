import React, { Suspense, useEffect, useState } from 'react';
import {
  Layout,
  Menu,
  Button,
  Card,
  Typography,
  Modal,
  Form,
  Input,
  message,
  Alert,
  Spin,
  Drawer,
  QRCode,
  Badge,
} from 'antd';
import {
  HomeOutlined,
  TeamOutlined,
  KeyOutlined,
  CloudServerOutlined,
  BarChartOutlined,
  BugOutlined,
  SafetyCertificateOutlined,
  DashboardOutlined,
  FieldTimeOutlined,
  FileTextOutlined,
  UsergroupAddOutlined,
  LockOutlined,
  GlobalOutlined,
  SafetyCertificateOutlined as AuditOutlined,
  SearchOutlined,
  MessageOutlined,
} from '@ant-design/icons';
import { useNavigate, useLocation, Outlet, Link } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { logout, logoutKeyPortal, apiClient } from '../api/client';
import {
  adminKeysQueryOptions,
  keyNameOptionsQueryOptions,
  organizationOptionsQueryOptions,
} from '../api/adminQueries';
import { gatewayConfigQueryOptions } from '../config/gateway';
import { useI18n } from '../i18n';
import { AccountMenu } from './AccountMenu';
import { TaskNavigation } from './TaskNavigation';
import { getNavigationGroups, getSelectedNavigationPath } from './navigation';
import { SidebarToggle } from './SidebarToggle';
import { useSidebar } from './useSidebar';
import './sidebar.css';
import { preloadRoute } from './lazyRoute';
import { useSecurityEventUnread } from '../features/security/useSecurityEventUnread';
import { GlobalSearch } from '../features/search/GlobalSearch';
import { ConfigurationFeedback } from '../features/diagnostics/ConfigurationFeedback';
import { FirstKeyGuide } from '../features/onboarding/FirstKeyGuide';

const { Header, Content, Sider } = Layout;
const { Text } = Typography;
const navigationIcons: Record<string, React.ReactNode> = {
  home: <HomeOutlined />,
  team: <TeamOutlined />,
  key: <KeyOutlined />,
  providers: <CloudServerOutlined />,
  limits: <FieldTimeOutlined />,
  usage: <BarChartOutlined />,
  search: <SearchOutlined />,
  errors: <BugOutlined />,
  security: <AuditOutlined />,
  admins: <UsergroupAddOutlined />,
  status: <DashboardOutlined />,
  global: <GlobalOutlined />,
  message: <MessageOutlined />,
};

interface UserProfile {
  id: number;
  email: string;
  organization_id: number;
  role: string;
  status: string;
  totp_enabled?: boolean;
  totp_enabled_at?: string;
}

export const AppLayout: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [user, setUser] = useState<UserProfile | null>(() => {
    try {
      return JSON.parse(localStorage.getItem('user_info') || 'null');
    } catch {
      return null;
    }
  });
  const sidebar = useSidebar(location.key);
  const { t } = useI18n();
  const { unreadCount, markRead } = useSecurityEventUnread(user, location.pathname);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        event.altKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== 'b'
      )
        return;
      if (
        target instanceof HTMLElement &&
        target.closest(
          'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="dialog"]',
        )
      )
        return;
      event.preventDefault();
      sidebar.toggle();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [sidebar.toggle]);

  const fetchProfile = async () => {
    try {
      const res = await apiClient.get('/auth/me');
      if (res.data?.data) {
        setUser(res.data.data);
        localStorage.setItem('user_info', JSON.stringify(res.data.data));
      }
    } catch {
      // Keep the existing profile when the refresh or browser storage fails.
    }
  };

  useEffect(() => {
    let currentRole = '';
    try {
      currentRole = JSON.parse(localStorage.getItem('user_info') || '{}')?.role || '';
    } catch {
      // Treat missing or invalid stored user information as an unknown role.
    }
    if (currentRole !== 'key_viewer') fetchProfile();
  }, []);

  const warmNavigationPath = (path: string) => {
    preloadRoute(path);
    if (
      path === '/admin/keys' ||
      path === '/admin/usage' ||
      path === '/admin/requests' ||
      path === '/admin/rate-limits'
    ) {
      void queryClient.prefetchQuery(organizationOptionsQueryOptions());
    }
    if (path === '/admin/keys') {
      void queryClient.prefetchQuery(adminKeysQueryOptions());
      void queryClient.prefetchQuery(gatewayConfigQueryOptions());
    }
    if (path === '/admin/rate-limits' && user?.organization_id) {
      void queryClient.prefetchQuery(
        adminKeysQueryOptions({ page: 1, pageSize: 100, organizationId: user.organization_id }),
      );
    }
    if (path === '/admin/usage' || path === '/admin/requests') {
      void queryClient.prefetchQuery(keyNameOptionsQueryOptions());
    }
  };

  const menuItems = getNavigationGroups(user?.role).map(group => ({
    type: 'group' as const,
    key: group.key,
    label: t(group.label),
    children: group.items.map(item => ({
      key: item.path,
      icon: navigationIcons[item.icon],
      label:
        item.path === '/admin/security-events' ? (
          <span className="console-navigation-label">
            {t(item.label)}
            <Badge
              count={unreadCount}
              overflowCount={99}
              size="small"
              title={t('security.unread_events', 'Unread warning or higher events')}
            />
          </span>
        ) : (
          t(item.label)
        ),
      onMouseEnter: () => warmNavigationPath(item.path),
    })),
  }));

  const [changePasswordVisible, setChangePasswordVisible] = useState(false);
  const [passwordForm] = Form.useForm();
  const [submittingPassword, setSubmittingPassword] = useState(false);

  // MFA modal states
  const [mfaModalVisible, setMfaModalVisible] = useState(false);
  const [mfaSetupData, setMfaSetupData] = useState<{
    secret: string;
    otpauth_url: string;
    expires_in: number;
  } | null>(null);
  const [mfaLoading, setMfaLoading] = useState(false);
  const [mfaCode, setMfaCode] = useState('');
  const [disableMfaPassword, setDisableMfaPassword] = useState('');
  const [disableMfaCode, setDisableMfaCode] = useState('');
  const [disablingMfa, setDisablingMfa] = useState(false);

  const handleOpenMFA = async () => {
    setMfaModalVisible(true);
    setMfaCode('');
    setDisableMfaPassword('');
    setDisableMfaCode('');
    if (!user?.totp_enabled) {
      setMfaLoading(true);
      try {
        const res = await apiClient.post('/auth/mfa/setup');
        setMfaSetupData(res.data?.data || null);
      } catch (err: any) {
        message.error(err?.userMessage || 'Failed to initialize MFA setup');
      } finally {
        setMfaLoading(false);
      }
    }
  };

  const handleEnableMFA = async () => {
    if (!mfaCode || mfaCode.trim().length !== 6) {
      message.warning(t('auth.enter_code_to_verify', 'Please enter the 6-digit verification code'));
      return;
    }
    setMfaLoading(true);
    try {
      await apiClient.post('/auth/mfa/enable', { code: mfaCode.trim() });
      message.success(
        t('auth.mfa_enabled_success', 'Two-factor authentication enabled successfully!'),
      );
      setMfaModalVisible(false);
      fetchProfile();
    } catch (err: any) {
      message.error(err?.userMessage || t('common.error', 'Failed to enable MFA'));
    } finally {
      setMfaLoading(false);
    }
  };

  const handleDisableMFA = async () => {
    if (!disableMfaPassword || !disableMfaCode) {
      message.warning(
        t('auth.mfa_input_required', 'Please enter current password and 6-digit code'),
      );
      return;
    }
    setDisablingMfa(true);
    try {
      await apiClient.post('/auth/mfa/disable', {
        current_password: disableMfaPassword,
        code: disableMfaCode.trim(),
      });
      message.success(t('auth.mfa_disabled_success', 'Two-factor authentication disabled'));
      setMfaModalVisible(false);
      fetchProfile();
    } catch (err: any) {
      message.error(err?.userMessage || t('common.error', 'Failed to disable MFA'));
    } finally {
      setDisablingMfa(false);
    }
  };

  const handleLogout = async () => {
    if (user?.role === 'key_viewer') {
      await logoutKeyPortal();
    } else {
      await logout();
    }
  };

  const handleChangePassword = async (values: any) => {
    if (values.new_password !== values.confirm_password) {
      message.error(t('auth.pwd_not_match', 'The passwords do not match'));
      return;
    }
    setSubmittingPassword(true);
    try {
      const res = await apiClient.post('/auth/change-password', {
        current_password: values.current_password,
        new_password: values.new_password,
      });
      message.success(
        res.data?.data?.message ||
          t(
            'auth.change_pwd_success',
            'Password changed successfully. Please sign in with your new password.',
          ),
      );
      setChangePasswordVisible(false);
      passwordForm.resetFields();
      setTimeout(async () => {
        await handleLogout();
      }, 800);
    } catch {
      // Handled by interceptor
    } finally {
      setSubmittingPassword(false);
    }
  };

  const getPageTitle = (path: string) => {
    if (path.startsWith('/admin/keys/')) return t('nav.keys');
    switch (path) {
      case '/':
        return t('nav.overview', 'Overview');
      case '/key-portal':
        return t('nav.overview', 'Overview');
      case '/key-portal/usage':
        return t('keys.detail_tab_usage', 'Usage Details');
      case '/key-portal/ips':
        return t('keys.detail_tab_ip_usage', 'IP Usage');
      case '/key-portal/errors':
        return t('keys.detail_tab_errors', 'Error Logs');
      case '/key-portal/security':
        return t('keys.detail_tab_security', 'Security Audits');
      case '/admin/api-address':
        return t('nav.api_address');
      case '/admin/usage-reporting':
        return t('nav.usage_reporting');
      case '/admin/organizations':
        return t('nav.organizations', 'Organizations');
      case '/admin/keys':
        return t('nav.keys', 'Admin API Keys');
      case '/admin/providers':
        return t('nav.providers', 'Providers & Models');
      case '/admin/rate-limits':
        return t('nav.rate_limits', 'Rate Limits');
      case '/admin/usage':
        return t('nav.usage', 'Usage & Billing');
      case '/admin/errors':
        return t('nav.errors', 'Error Logs');
      case '/admin/request-logs':
        return t('nav.request_logs', 'Request Trace');
      case '/admin/requests':
        return t('ux.requests');
      case '/admin/security-events':
        return t('nav.security', 'Security Events');
      case '/admin/status':
        return t('nav.status', 'System Status');
      case '/admin/admin-users':
        return t('nav.admin_config', 'Admin Configuration');
      case '/admin/announcements':
        return t('announcements.manage');
      case '/admin/message-config':
        return t('nav.message_config', 'Message (OTP) Configuration');
      default:
        return 'BOS API Console';
    }
  };

  const currentTitle = getPageTitle(location.pathname);

  useEffect(() => {
    document.title = `${currentTitle} - BOS API`;
  }, [currentTitle]);

  const sidebarTop = (
    <div className="console-sidebar-top">
      <Link to="/" className="console-sidebar-brand" onClick={sidebar.closeMobile}>
        <img src="/logo.svg" alt="" />
        <span>BOS API</span>
      </Link>
    </div>
  );

  const sidebarNavigation = (
    <nav
      id="console-navigation"
      className="console-sidebar-nav"
      aria-label={t('ux.sidebar_navigation')}
      aria-hidden={!sidebar.expanded}
    >
      <Menu
        theme="light"
        mode="inline"
        // Only the outer sidebar collapses; menu items must not enter icon-only mode.
        inlineCollapsed={false}
        selectedKeys={[getSelectedNavigationPath(location.pathname)]}
        items={[
          ...menuItems,
          {
            key: '/docs/quickstart',
            icon: <FileTextOutlined />,
            label: (
              <a href="/docs/quickstart" target="_blank" rel="noopener noreferrer">
                {t('ux.help')}
              </a>
            ),
          },
        ]}
        onClick={({ key }) => {
          if (key !== '/docs/quickstart') {
            if (key === '/admin/security-events') markRead();
            warmNavigationPath(key);
            navigate(key);
          }
          sidebar.closeMobile();
        }}
      />
    </nav>
  );

  return (
    <Layout
      className="console-shell"
      data-sidebar-expanded={sidebar.expanded}
      data-mobile={sidebar.isMobile}
      style={{ minHeight: '100vh', background: '#f7f7f7' }}
    >
      <div className="console-sidebar-toggle-anchor">
        <SidebarToggle expanded={sidebar.expanded} onClick={sidebar.toggle} />
      </div>
      {sidebar.isMobile ? (
        <Drawer
          rootClassName="console-sidebar-drawer"
          placement="left"
          width={280}
          open={sidebar.mobileOpen}
          onClose={sidebar.closeMobile}
          title={sidebarTop}
          closable={false}
        >
          {sidebarNavigation}
        </Drawer>
      ) : (
        <Sider
          className="console-sidebar"
          width={240}
          theme="light"
          collapsedWidth={0}
          collapsed={sidebar.desktopCollapsed}
          trigger={null}
          aria-hidden={sidebar.desktopCollapsed}
        >
          <div
            className="console-sidebar-content"
            {...(sidebar.desktopCollapsed ? { inert: '' } : {})}
          >
            {sidebarTop}
            {sidebarNavigation}
          </div>
        </Sider>
      )}

      <Layout className="console-main" style={{ background: '#f7f7f7' }}>
        <Header
          className="console-header"
          style={{
            background: '#ffffff',
            padding: '0 24px',
            height: 56,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            borderBottom: '1px solid #e5e5e5',
            position: 'sticky',
            top: 0,
            zIndex: 50,
            boxSizing: 'border-box',
          }}
        >
          <div className="console-header-leading">
            <Text
              className="console-page-title"
              strong
              style={{ fontSize: 15, color: '#171717', letterSpacing: 0, lineHeight: 1 }}
            >
              {currentTitle}
            </Text>
          </div>
          <GlobalSearch role={user?.role} organizationID={user?.organization_id} />
          <AccountMenu
            user={user}
            onMFA={handleOpenMFA}
            onChangePassword={() => {
              passwordForm.resetFields();
              setChangePasswordVisible(true);
            }}
            onLogout={handleLogout}
          />
        </Header>

        <Content
          className="console-content"
          style={{ margin: '24px 28px', minHeight: 'calc(100vh - 104px)' }}
        >
          <ConfigurationFeedback />
          <TaskNavigation role={user?.role} />
          <FirstKeyGuide user={user} />
          <Suspense
            fallback={
              <Card
                role="status"
                aria-busy="true"
                aria-label={t('common.loading')}
                style={{ minHeight: 320 }}
              />
            }
          >
            <Outlet context={{ user }} />
          </Suspense>
        </Content>

        {/* Change Password Modal */}
        <Modal
          title={
            <div style={{ fontWeight: 600, fontSize: 16, color: '#171717' }}>
              {t('auth.change_pwd_modal_title', 'Change Password')}
            </div>
          }
          open={changePasswordVisible}
          onCancel={() => {
            setChangePasswordVisible(false);
            passwordForm.resetFields();
          }}
          footer={null}
          width={420}
          destroyOnHidden
        >
          <p style={{ color: '#737373', fontSize: 13, marginTop: 4, marginBottom: 20 }}>
            {t(
              'auth.change_pwd_modal_desc',
              'Update your account password. All active sessions will be invalidated and you will be prompted to sign in again.',
            )}
          </p>

          <Form form={passwordForm} layout="vertical" onFinish={handleChangePassword}>
            <Form.Item
              name="current_password"
              label={t('auth.current_pwd_label', 'Current Password')}
              rules={[
                {
                  required: true,
                  message: t('auth.enter_current_pwd', 'Please enter your current password'),
                },
              ]}
            >
              <Input.Password
                prefix={<LockOutlined style={{ color: '#8c8c8c' }} />}
                placeholder={t('auth.current_pwd_placeholder', 'Enter current password')}
                size="large"
              />
            </Form.Item>

            <Form.Item
              name="new_password"
              label={t('auth.new_pwd_label', 'New Password')}
              rules={[
                { required: true, message: t('auth.enter_new_pwd', 'Please enter a new password') },
                {
                  min: 12,
                  message: t('auth.pwd_min_length', 'Password must be at least 12 characters'),
                },
              ]}
            >
              <Input.Password
                prefix={<LockOutlined style={{ color: '#8c8c8c' }} />}
                placeholder={t('auth.new_pwd_placeholder', 'Minimum 12 characters')}
                size="large"
              />
            </Form.Item>

            <Form.Item
              name="confirm_password"
              label={t('auth.confirm_pwd_label', 'Confirm New Password')}
              dependencies={['new_password']}
              rules={[
                {
                  required: true,
                  message: t('auth.confirm_new_pwd', 'Please confirm your new password'),
                },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('new_password') === value) {
                      return Promise.resolve();
                    }
                    return Promise.reject(
                      new Error(t('auth.pwd_not_match', 'The passwords do not match')),
                    );
                  },
                }),
              ]}
            >
              <Input.Password
                prefix={<LockOutlined style={{ color: '#8c8c8c' }} />}
                placeholder={t('auth.confirm_pwd_placeholder', 'Re-enter your new password')}
                size="large"
              />
            </Form.Item>

            <Form.Item style={{ marginTop: 24, marginBottom: 8 }}>
              <Button
                type="primary"
                htmlType="submit"
                size="large"
                block
                loading={submittingPassword}
                style={{
                  height: 40,
                  borderRadius: 8,
                  fontWeight: 500,
                  fontSize: 14,
                }}
              >
                {t('auth.btn_update_pwd', 'Update Password')}
              </Button>
            </Form.Item>
          </Form>
        </Modal>

        {/* Two-Factor MFA Modal */}
        <Modal
          title={
            <div
              style={{
                fontWeight: 600,
                fontSize: 16,
                color: '#171717',
                display: 'flex',
                alignItems: 'center',
                gap: 8,
              }}
            >
              <SafetyCertificateOutlined
                style={{ color: user?.totp_enabled ? 'var(--success)' : 'var(--ink)' }}
              />
              {user?.totp_enabled
                ? t('auth.mfa_modal_title_active', 'Two-Factor Authentication (Active)')
                : t('auth.mfa_modal_title_enable', 'Enable Two-Factor Authentication')}
            </div>
          }
          open={mfaModalVisible}
          onCancel={() => {
            setMfaModalVisible(false);
            setMfaCode('');
            setDisableMfaPassword('');
            setDisableMfaCode('');
          }}
          footer={null}
          width={460}
          destroyOnHidden
        >
          {user?.totp_enabled ? (
            <div>
              <Alert
                type="success"
                showIcon
                message={t('auth.mfa_active_alert_title', 'Two-Factor Authentication is Enabled')}
                description={t(
                  'auth.mfa_active_alert_desc',
                  'Your account is protected with TOTP two-step verification. An authenticator code is required upon signing in.',
                )}
                style={{ marginBottom: 20, borderRadius: 8 }}
              />

              <div style={{ padding: '16px 0', borderTop: '1px solid #f0f0f0' }}>
                <Text strong style={{ display: 'block', marginBottom: 6, color: 'var(--error)' }}>
                  {t('auth.mfa_disable_heading', 'Disable Two-Factor Authentication')}
                </Text>
                <Text
                  style={{ fontSize: 13, color: '#737373', display: 'block', marginBottom: 16 }}
                >
                  {t(
                    'auth.mfa_disable_desc',
                    'To deactivate two-factor authentication, confirm your current account password and current 6-digit code.',
                  )}
                </Text>

                <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                  <div>
                    <label
                      style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}
                    >
                      {t('auth.current_pwd_label', 'Current Password')}
                    </label>
                    <Input.Password
                      placeholder={t('auth.current_pwd_placeholder', 'Enter current password')}
                      value={disableMfaPassword}
                      onChange={e => setDisableMfaPassword(e.target.value)}
                      size="large"
                    />
                  </div>

                  <div>
                    <label
                      style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}
                    >
                      {t('auth.mfa_code_label', '6-Digit Authenticator Code')}
                    </label>
                    <Input
                      placeholder="000000"
                      maxLength={6}
                      value={disableMfaCode}
                      onChange={e =>
                        setDisableMfaCode(e.target.value.replace(/\D/g, '').slice(0, 6))
                      }
                      size="large"
                      style={{ letterSpacing: 4, textAlign: 'center' }}
                    />
                  </div>

                  <Button
                    danger
                    type="primary"
                    size="large"
                    loading={disablingMfa}
                    onClick={handleDisableMFA}
                    style={{ marginTop: 8, height: 40, borderRadius: 8 }}
                  >
                    {t('auth.btn_deactivate_mfa', 'Deactivate MFA')}
                  </Button>
                </div>
              </div>
            </div>
          ) : (
            <div>
              {mfaLoading && !mfaSetupData ? (
                <div style={{ textAlign: 'center', padding: '40px 0' }}>
                  <Spin tip={t('auth.mfa_generating', 'Generating secure MFA credentials...')} />
                </div>
              ) : (
                <div>
                  <p style={{ color: '#737373', fontSize: 13, marginTop: 4, marginBottom: 16 }}>
                    {t(
                      'auth.mfa_scan_guide',
                      'Scan the QR code below using your authenticator app (Google Authenticator, Microsoft Authenticator, 1Password, etc.).',
                    )}
                  </p>

                  {mfaSetupData && (
                    <div
                      style={{
                        display: 'flex',
                        flexDirection: 'column',
                        alignItems: 'center',
                        marginBottom: 20,
                      }}
                    >
                      <div
                        style={{
                          padding: 12,
                          background: '#ffffff',
                          borderRadius: 12,
                          border: '1px solid #e5e5e5',
                          boxShadow: '0 2px 8px rgba(0,0,0,0.04)',
                          marginBottom: 16,
                        }}
                      >
                        <QRCode value={mfaSetupData.otpauth_url} size={180} />
                      </div>

                      <div
                        style={{
                          width: '100%',
                          background: '#f8fafc',
                          border: '1px solid #e2e8f0',
                          borderRadius: 8,
                          padding: '10px 14px',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                        }}
                      >
                        <div>
                          <div style={{ fontSize: 11, color: '#64748b', fontWeight: 500 }}>
                            {t('auth.mfa_manual_entry', 'MANUAL KEY ENTRY')}
                          </div>
                          <Text
                            code
                            copyable={{ text: mfaSetupData.secret }}
                            style={{ fontSize: 13, fontWeight: 600 }}
                          >
                            {mfaSetupData.secret}
                          </Text>
                        </div>
                      </div>
                    </div>
                  )}

                  <div style={{ marginTop: 16 }}>
                    <label
                      style={{ fontSize: 13, fontWeight: 600, display: 'block', marginBottom: 6 }}
                    >
                      {t('auth.mfa_enter_code', 'Enter 6-digit confirmation code:')}
                    </label>
                    <Input
                      placeholder="000000"
                      maxLength={6}
                      value={mfaCode}
                      onChange={e => setMfaCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                      size="large"
                      style={{
                        letterSpacing: 6,
                        textAlign: 'center',
                        fontSize: 20,
                        height: 46,
                        marginBottom: 16,
                      }}
                    />

                    <Button
                      type="primary"
                      size="large"
                      block
                      loading={mfaLoading}
                      onClick={handleEnableMFA}
                      style={{
                        height: 40,
                        borderRadius: 8,
                        fontWeight: 500,
                        fontSize: 14,
                      }}
                    >
                      {t('auth.btn_activate_mfa', 'Activate MFA')}
                    </Button>
                  </div>
                </div>
              )}
            </div>
          )}
        </Modal>
      </Layout>
    </Layout>
  );
};
