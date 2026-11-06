import React, { useState, useEffect } from 'react';
import {
  Card,
  Form,
  Input,
  Button,
  Typography,
  message,
  Alert,
  Modal,
  Space,
  Tabs,
  QRCode,
} from 'antd';
import {
  UserOutlined,
  LockOutlined,
  MailOutlined,
  SafetyCertificateOutlined,
  CopyOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { apiClient } from '../../api/client';
import { copyText } from '../common/copyText';
import { useI18n, LocaleSettingsControl } from '../../i18n';

const { Title, Text } = Typography;

export const LoginPage: React.FC = () => {
  const navigate = useNavigate();
  const { t } = useI18n();
  const [loginForm] = Form.useForm();
  const [keyLoginForm] = Form.useForm();
  const [forgotForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [keyLoginLoading, setKeyLoginLoading] = useState(false);
  const [loginMode, setLoginMode] = useState<'account' | 'api-key'>('account');

  // Forgot password modal states
  const [forgotVisible, setForgotVisible] = useState(false);
  const [sendingOtp, setSendingOtp] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [otpSent, setOtpSent] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const [devCode, setDevCode] = useState('');

  useEffect(() => {
    document.title = `${t('auth.login_title', 'Sign In')} - BOS API`;
    // If already logged in, redirect
    if (localStorage.getItem('auth_token')) {
      navigate('/');
    }
  }, [navigate, t]);

  useEffect(() => {
    let timer: any;
    if (countdown > 0) {
      timer = setInterval(() => setCountdown(prev => prev - 1), 1000);
    }
    return () => clearInterval(timer);
  }, [countdown]);

  // MFA states
  const [mfaPending, setMfaPending] = useState(false);
  const [mfaSetupRequired, setMfaSetupRequired] = useState(false);
  const [mfaSecret, setMfaSecret] = useState('');
  const [otpauthUrl, setOtpauthUrl] = useState('');
  const [mfaToken, setMfaToken] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [mfaLoading, setMfaLoading] = useState(false);

  const handleAuthSuccess = (pair: any) => {
    if (pair?.access_token) {
      localStorage.setItem('auth_token', pair.access_token);
      if (pair.refresh_token) {
        localStorage.setItem('refresh_token', pair.refresh_token);
      }
      if (pair.user) {
        localStorage.setItem('user_info', JSON.stringify(pair.user));
      }
      message.success(t('auth.login_success', 'Signed in successfully'));
      navigate('/');
    } else {
      message.error(t('auth.invalid_server_resp', 'Invalid response from server'));
    }
  };

  const onFinish = async (values: { email: string; password: string }) => {
    setLoading(true);
    try {
      const payload = {
        email: values.email ? values.email.trim() : '',
        password: values.password,
      };
      const res = await apiClient.post('/auth/login', payload);
      const data = res.data?.data;
      if (data?.mfa_required) {
        setMfaToken(data.mfa_token);
        setMfaPending(true);
        setMfaSetupRequired(Boolean(data.mfa_setup_required));
        setMfaSecret(data.secret || '');
        setOtpauthUrl(data.otpauth_url || '');
        setMfaCode('');
        if (data.mfa_setup_required) {
          message.info(
            t(
              'auth.mfa_setup_policy_required',
              'Administrator security policy requires two-factor authentication setup',
            ),
          );
        } else {
          message.info(t('auth.mfa_required', 'Two-factor authentication required'));
        }
        return;
      }
      handleAuthSuccess(data);
    } catch {
      // Error messages are handled by apiClient interceptor
    } finally {
      setLoading(false);
    }
  };

  const onKeyLogin = async (values: { api_key: string }) => {
    setKeyLoginLoading(true);
    try {
      const response = await apiClient.post(
        '/auth/key-login',
        { api_key: values.api_key },
        { withCredentials: true },
      );
      const session = response.data?.data;
      if (!session?.access_token || !session?.key) throw new Error('invalid response');
      localStorage.removeItem('auth_token');
      localStorage.removeItem('refresh_token');
      localStorage.setItem(
        'user_info',
        JSON.stringify({
          ...session.key,
          role: 'key_viewer',
          email: session.key.name || 'API Key',
        }),
      );
      localStorage.setItem('key_portal_token', session.access_token);
      localStorage.setItem('key_portal_info', JSON.stringify(session.key));
      message.success(t('auth.key_login_success', 'API key sign-in successful'));
      navigate('/key-portal');
    } catch {
      // Error messages are handled by apiClient interceptor.
    } finally {
      setKeyLoginLoading(false);
    }
  };

  const handleVerifyMFA = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    if (!mfaCode || mfaCode.trim().length !== 6) {
      message.warning(t('auth.enter_code_to_verify', 'Please enter the 6-digit verification code'));
      return;
    }
    setMfaLoading(true);
    try {
      const res = await apiClient.post('/auth/mfa/verify', {
        mfa_token: mfaToken,
        code: mfaCode.trim(),
      });
      handleAuthSuccess(res.data?.data);
    } catch {
      // Handled by interceptor
    } finally {
      setMfaLoading(false);
    }
  };

  const handleSendOtp = async () => {
    try {
      const values = await forgotForm.validateFields(['email']);
      setSendingOtp(true);
      const res = await apiClient.post('/auth/forgot-password', { email: values.email.trim() });
      message.success(
        res.data?.data?.message || t('auth.verification_code_sent', 'Verification code sent'),
      );
      setOtpSent(true);
      setCountdown(60);
      if (res.data?.data?.dev_code) {
        setDevCode(res.data.data.dev_code);
      }
    } catch {
      // Validation error or handled by interceptor
    } finally {
      setSendingOtp(false);
    }
  };

  const handleResetPassword = async (values: any) => {
    if (values.new_password !== values.confirm_password) {
      message.error(t('auth.pwd_not_match', 'The passwords do not match'));
      return;
    }
    setResetting(true);
    try {
      const res = await apiClient.post('/auth/reset-password', {
        email: values.email.trim(),
        otp: values.otp.trim(),
        new_password: values.new_password,
      });
      message.success(
        res.data?.data?.message ||
          t(
            'auth.change_pwd_success',
            'Password reset successfully. Please sign in with your new password.',
          ),
      );
      setForgotVisible(false);
      loginForm.setFieldValue('email', values.email.trim());
      loginForm.setFieldValue('password', '');
      forgotForm.resetFields();
      setOtpSent(false);
      setDevCode('');
    } catch {
      // Handled by interceptor
    } finally {
      setResetting(false);
    }
  };

  return (
    <div
      style={{
        position: 'relative',
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        minHeight: '100vh',
        background: '#f7f7f7',
        padding: 20,
      }}
    >
      <div style={{ position: 'absolute', top: 20, right: 24 }}>
        <LocaleSettingsControl />
      </div>

      <Card
        style={{
          width: 400,
          padding: 8,
        }}
      >
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <img
            src="/logo.svg"
            alt="BOS API"
            style={{
              width: 48,
              height: 48,
              marginBottom: 14,
              display: 'inline-block',
            }}
          />
          <Title
            level={3}
            style={{ margin: 0, fontWeight: 700, letterSpacing: 0, color: '#171717' }}
          >
            {t('auth.login_title', 'BOS API Console')}
          </Title>
          <Text style={{ color: '#737373', fontSize: 13, marginTop: 4, display: 'inline-block' }}>
            {t('auth.sub_title', 'Sign in with your enterprise credentials')}
          </Text>
        </div>

        {mfaPending ? (
          <div>
            {mfaSetupRequired ? (
              <>
                <div style={{ textAlign: 'center', marginBottom: 16 }}>
                  <SafetyCertificateOutlined
                    style={{ fontSize: 40, color: 'var(--ink)', marginBottom: 8 }}
                  />
                  <Title level={4} style={{ margin: 0, fontWeight: 600, color: '#171717' }}>
                    {t(
                      'auth.mfa_admin_setup_title',
                      'Administrator Two-Factor Authentication Setup',
                    )}
                  </Title>
                  <Text
                    style={{
                      color: '#737373',
                      fontSize: 13,
                      marginTop: 4,
                      display: 'inline-block',
                    }}
                  >
                    {t(
                      'auth.mfa_admin_setup_desc',
                      'Platform security policy requires administrator accounts to set up two-factor authentication. Scan the QR code using your authenticator app.',
                    )}
                  </Text>
                </div>

                {otpauthUrl && (
                  <div
                    style={{
                      display: 'flex',
                      flexDirection: 'column',
                      alignItems: 'center',
                      marginBottom: 16,
                    }}
                  >
                    <div
                      style={{
                        padding: 10,
                        background: '#ffffff',
                        borderRadius: 8,
                        border: '1px solid #e5e5e5',
                        boxShadow: '0 2px 8px rgba(0,0,0,0.04)',
                        marginBottom: 10,
                      }}
                    >
                      <QRCode value={otpauthUrl} size={160} />
                    </div>
                    {mfaSecret && (
                      <div
                        style={{
                          width: '100%',
                          padding: '6px 12px',
                          background: '#f5f5f5',
                          borderRadius: 6,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          marginBottom: 4,
                        }}
                      >
                        <div style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                          <Text type="secondary" style={{ fontSize: 11, display: 'block' }}>
                            {t('auth.mfa_manual_entry_key', 'Manual entry key:')}
                          </Text>
                          <Text
                            copyable={{ text: mfaSecret }}
                            code
                            style={{ fontSize: 12, fontWeight: 600 }}
                          >
                            {mfaSecret}
                          </Text>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                <form onSubmit={handleVerifyMFA}>
                  <div style={{ marginBottom: 16 }}>
                    <Text
                      style={{ fontSize: 12, color: '#595959', display: 'block', marginBottom: 6 }}
                    >
                      {t(
                        'auth.mfa_enter_code_app',
                        'Enter the 6-digit verification code from your authenticator app:',
                      )}
                    </Text>
                    <Input
                      prefix={<SafetyCertificateOutlined style={{ color: '#8c8c8c' }} />}
                      aria-label={t('auth.mfa_code_label', '6-Digit Verification Code')}
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      value={mfaCode}
                      onChange={e => setMfaCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                      size="large"
                      maxLength={6}
                      autoFocus
                      style={{ textAlign: 'center', fontSize: 24, letterSpacing: 8, height: 48 }}
                    />
                  </div>

                  <Button
                    type="primary"
                    htmlType="submit"
                    size="large"
                    block
                    loading={mfaLoading}
                    style={{
                      height: 40,
                      borderRadius: 8,
                      fontWeight: 500,
                      fontSize: 14,
                      marginBottom: 12,
                    }}
                  >
                    {t('auth.btn_verify_complete', 'Verify & Complete Sign In')}
                  </Button>

                  <div style={{ textAlign: 'center' }}>
                    <Button
                      type="link"
                      style={{ padding: 0, fontSize: 13, color: '#525252' }}
                      onClick={() => {
                        setMfaPending(false);
                        setMfaSetupRequired(false);
                        setMfaSecret('');
                        setOtpauthUrl('');
                        setMfaToken('');
                        setMfaCode('');
                      }}
                    >
                      {t('auth.btn_back_to_login', 'Back to standard sign in')}
                    </Button>
                  </div>
                </form>
              </>
            ) : (
              <>
                <div style={{ textAlign: 'center', marginBottom: 20 }}>
                  <SafetyCertificateOutlined
                    style={{ fontSize: 44, color: 'var(--ink)', marginBottom: 12 }}
                  />
                  <Title level={4} style={{ margin: 0, fontWeight: 600, color: '#171717' }}>
                    {t('auth.mfa_title', 'Two-Factor Authentication')}
                  </Title>
                  <Text
                    style={{
                      color: '#737373',
                      fontSize: 13,
                      marginTop: 4,
                      display: 'inline-block',
                    }}
                  >
                    {t('auth.mfa_sub_title', 'Enter the 6-digit code from your authenticator app')}
                  </Text>
                </div>

                <form onSubmit={handleVerifyMFA}>
                  <div style={{ marginBottom: 20 }}>
                    <Input
                      prefix={<SafetyCertificateOutlined style={{ color: '#8c8c8c' }} />}
                      aria-label={t('auth.mfa_code_label', '6-Digit Verification Code')}
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      value={mfaCode}
                      onChange={e => setMfaCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                      size="large"
                      maxLength={6}
                      autoFocus
                      style={{ textAlign: 'center', fontSize: 24, letterSpacing: 8, height: 48 }}
                    />
                  </div>

                  <Button
                    type="primary"
                    htmlType="submit"
                    size="large"
                    block
                    loading={mfaLoading}
                    style={{
                      height: 40,
                      borderRadius: 8,
                      fontWeight: 500,
                      fontSize: 14,
                      marginBottom: 12,
                    }}
                  >
                    {t('auth.btn_verify_signin', 'Verify & Sign In')}
                  </Button>

                  <div style={{ textAlign: 'center' }}>
                    <Button
                      type="link"
                      style={{ padding: 0, fontSize: 13, color: '#525252' }}
                      onClick={() => {
                        setMfaPending(false);
                        setMfaSetupRequired(false);
                        setMfaSecret('');
                        setOtpauthUrl('');
                        setMfaToken('');
                        setMfaCode('');
                      }}
                    >
                      {t('auth.btn_back_to_login', 'Back to standard sign in')}
                    </Button>
                  </div>
                </form>
              </>
            )}
          </div>
        ) : (
          <>
            <Tabs
              activeKey={loginMode}
              onChange={value => setLoginMode(value as 'account' | 'api-key')}
              items={[
                { key: 'account', label: t('auth.account_login_tab', 'Account'), children: null },
                { key: 'api-key', label: t('auth.api_key_login_tab', 'API Key'), children: null },
              ]}
              style={{ marginBottom: 20 }}
            />
            {loginMode === 'account' ? (
              <>
                <Alert
                  type="info"
                  showIcon
                  message={t('auth.default_admin_title', 'Default Administrator')}
                  description={t(
                    'auth.default_admin_desc',
                    'In development, sign in with your configured admin email and password (minimum 12 characters).',
                  )}
                  style={{
                    marginBottom: 20,
                    borderRadius: 8,
                    background: '#eff6ff',
                    border: '1px solid #bfdbfe',
                  }}
                />

                <Form form={loginForm} layout="vertical" onFinish={onFinish}>
                  <Form.Item
                    name="email"
                    label={t('auth.email', 'Email Address')}
                    rules={[
                      {
                        required: true,
                        message: t('auth.email_placeholder', 'Please enter your email'),
                      },
                      {
                        type: 'email',
                        message: t('auth.email_placeholder', 'Please enter a valid email'),
                      },
                    ]}
                  >
                    <Input
                      prefix={<UserOutlined style={{ color: '#8c8c8c' }} />}
                      placeholder="admin@bosapi.net"
                      size="large"
                    />
                  </Form.Item>

                  <Form.Item
                    name="password"
                    label={t('auth.password', 'Password')}
                    rules={[
                      {
                        required: true,
                        message: t('auth.password_placeholder', 'Please enter your password'),
                      },
                      {
                        min: 12,
                        message: t(
                          'auth.pwd_min_length',
                          'Password must be at least 12 characters',
                        ),
                      },
                    ]}
                  >
                    <Input.Password
                      prefix={<LockOutlined style={{ color: '#8c8c8c' }} />}
                      placeholder="Minimum 12 characters"
                      size="large"
                    />
                  </Form.Item>

                  <div
                    style={{
                      display: 'flex',
                      justifyContent: 'flex-end',
                      marginTop: -8,
                      marginBottom: 16,
                    }}
                  >
                    <Button
                      type="link"
                      style={{ padding: 0, fontSize: 13, color: '#525252' }}
                      onClick={() => {
                        forgotForm.resetFields();
                        setOtpSent(false);
                        setDevCode('');
                        const currentEmail = loginForm.getFieldValue('email');
                        if (currentEmail) {
                          forgotForm.setFieldValue('email', currentEmail);
                        }
                        setForgotVisible(true);
                      }}
                    >
                      {t('auth.forgot_password', 'Forgot password?')}
                    </Button>
                  </div>

                  <Form.Item style={{ marginBottom: 8 }}>
                    <Button
                      type="primary"
                      htmlType="submit"
                      size="large"
                      block
                      loading={loading}
                      style={{
                        height: 40,
                        borderRadius: 8,
                        fontWeight: 500,
                        fontSize: 14,
                      }}
                    >
                      {t('auth.sign_in', 'Sign In')}
                    </Button>
                  </Form.Item>
                </Form>
              </>
            ) : (
              <>
                <Form form={keyLoginForm} layout="vertical" onFinish={onKeyLogin}>
                  <Form.Item
                    name="api_key"
                    label={t('auth.api_key_label', 'API Key')}
                    rules={[
                      {
                        required: true,
                        whitespace: true,
                        message: t('auth.api_key_required', 'Please enter your API key'),
                      },
                    ]}
                  >
                    <Input.Password
                      prefix={<KeyOutlined style={{ color: '#8c8c8c' }} />}
                      visibilityToggle={false}
                      autoComplete="off"
                      placeholder={t('auth.api_key_placeholder', 'Enter API key')}
                      size="large"
                    />
                  </Form.Item>
                  <Button
                    type="primary"
                    htmlType="submit"
                    size="large"
                    block
                    loading={keyLoginLoading}
                  >
                    {t('auth.api_key_sign_in', 'Sign in with API Key')}
                  </Button>
                </Form>
              </>
            )}
          </>
        )}
      </Card>

      {/* Forgot Password Modal */}
      <Modal
        title={
          <div style={{ fontWeight: 600, fontSize: 16, color: '#171717' }}>
            {t('auth.reset_pwd_title', 'Reset Password')}
          </div>
        }
        open={forgotVisible}
        onCancel={() => {
          setForgotVisible(false);
          forgotForm.resetFields();
          setOtpSent(false);
          setDevCode('');
        }}
        footer={null}
        width={440}
        destroyOnHidden
      >
        <p style={{ color: '#737373', fontSize: 13, marginTop: 4, marginBottom: 20 }}>
          {t(
            'auth.reset_pwd_desc',
            'Enter your registered email address to receive a 6-digit verification code to reset your password.',
          )}
        </p>

        <Form form={forgotForm} layout="vertical" onFinish={handleResetPassword}>
          <Form.Item
            label={t('auth.email', 'Email Address')}
            htmlFor="forgot-password-email"
            required
          >
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item
                name="email"
                noStyle
                rules={[
                  {
                    required: true,
                    message: t('auth.email_placeholder', 'Please enter your email'),
                  },
                  {
                    type: 'email',
                    message: t('auth.email_placeholder', 'Please enter a valid email'),
                  },
                ]}
              >
                <Input
                  id="forgot-password-email"
                  prefix={<MailOutlined style={{ color: '#8c8c8c' }} />}
                  placeholder="user@example.com"
                  size="large"
                  style={{ borderRadius: '8px 0 0 8px' }}
                />
              </Form.Item>
              <Button
                size="large"
                type="primary"
                onClick={handleSendOtp}
                loading={sendingOtp}
                disabled={countdown > 0}
                style={{ borderRadius: '0 8px 8px 0', minWidth: 110 }}
              >
                {countdown > 0
                  ? `${countdown}s`
                  : otpSent
                    ? t('adminusers.btn_resend_otp', 'Resend')
                    : t('adminusers.btn_send_otp', 'Send Code')}
              </Button>
            </Space.Compact>
          </Form.Item>

          {devCode && (
            <Alert
              type="warning"
              showIcon
              message={
                <div
                  style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
                >
                  <span>
                    {t('auth.dev_otp_label', 'Development OTP:')} <strong>{devCode}</strong>
                  </span>
                  <Button
                    size="small"
                    type="link"
                    icon={<CopyOutlined />}
                    onClick={async () => {
                      forgotForm.setFieldValue('otp', devCode);
                      if (await copyText(devCode)) {
                        message.success(t('auth.code_copied_filled', 'Code copied and filled'));
                      } else {
                        message.warning(t('auth.code_filled_not_copied'));
                      }
                    }}
                  >
                    {t('auth.fill_code', 'Fill')}
                  </Button>
                </div>
              }
              style={{ marginBottom: 16, borderRadius: 8 }}
            />
          )}

          {otpSent && (
            <>
              <Form.Item
                name="otp"
                label={t('auth.mfa_code_label', '6-Digit Verification Code')}
                rules={[
                  {
                    required: true,
                    message: t('auth.enter_code_to_verify', 'Please enter the 6-digit code'),
                  },
                  { len: 6, message: t('auth.code_must_be_6_digits', 'Code must be 6 digits') },
                ]}
              >
                <Input
                  prefix={<SafetyCertificateOutlined style={{ color: '#8c8c8c' }} />}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder={t('auth.code_placeholder', 'Enter 6-digit code')}
                  maxLength={6}
                  size="large"
                />
              </Form.Item>

              <Form.Item
                name="new_password"
                label={t('auth.new_pwd_label', 'New Password')}
                rules={[
                  {
                    required: true,
                    message: t('auth.enter_new_pwd', 'Please enter a new password'),
                  },
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
                  loading={resetting}
                  style={{
                    height: 40,
                    borderRadius: 8,
                    fontWeight: 500,
                    fontSize: 14,
                  }}
                >
                  {t('auth.btn_reset_pwd', 'Reset Password')}
                </Button>
              </Form.Item>
            </>
          )}
        </Form>
      </Modal>
    </div>
  );
};

export default LoginPage;
