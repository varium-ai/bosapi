import { ConsoleToolbar } from '../common/ConsoleToolbar';
import { TableAction, TableActionMenu } from '../common/TableAction';
import { LoadError, StatusTag } from '../common/ConsoleUX';
import { SearchInput } from '../common/SearchInput';
import { ResourceName } from '../common/ResourceName';
import { generatePassword } from '../common/generatePassword';
import { copyText } from '../common/copyText';
import React, { lazy, Suspense, useEffect, useRef, useState } from 'react';
import {
  Card,
  Pagination,
  Tag,
  Space,
  Button,
  Input,
  Select,
  Modal,
  Form,
  Typography,
  message,
  Alert,
  Row,
  Col,
  Tooltip,
} from 'antd';
import {
  UserSwitchOutlined,
  PlusOutlined,
  MailOutlined,
  SafetyCertificateOutlined,
  KeyOutlined,
  CheckOutlined,
} from '@ant-design/icons';
import { RefreshButton } from '../common/RefreshButton';
import client from '../../api/client';
import { apiClient } from '../../api/client';
import { useQuery } from '@tanstack/react-query';
import { useI18n } from '../../i18n';
import type { AdminLoginRecord } from './AdminLoginRecordsModal';

const loadLoginRecordsModal = () => import('./AdminLoginRecordsModal');
const AdminLoginRecordsModal = lazy(loadLoginRecordsModal);

const { Text } = Typography;

interface AdminUser {
  id: number;
  organization_id: number;
  email: string;
  role: string;
  status: string;
  totp_enabled?: boolean;
  login_locked?: boolean;
  login_locked_until?: string;
  created_at?: string;
  updated_at?: string;
}

interface OrganizationOption {
  id: number;
  name: string;
  status: string;
}

export const AdminUsersPage: React.FC = () => {
  const { t, language, formatDateTime, timezoneBadge } = useI18n();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const usersRequest = useRef(0);
  const pendingActions = useRef({ create: false, otp: false, resetPassword: false, role: false });

  const [roleFilter, setRoleFilter] = useState<string>('');
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState<string>('');

  const [modalOpen, setModalOpen] = useState(false);
  const [sendingOtp, setSendingOtp] = useState(false);
  const [creating, setCreating] = useState(false);
  const [otpSent, setOtpSent] = useState(false);
  const [otpError, setOtpError] = useState(false);

  // Reset password states
  const [resetModalOpen, setResetModalOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);
  const [resettingPassword, setResettingPassword] = useState(false);
  const [resetForm] = Form.useForm();

  // Change role states
  const [roleModalOpen, setRoleModalOpen] = useState(false);
  const [roleTargetUser, setRoleTargetUser] = useState<AdminUser | null>(null);
  const [updatingRole, setUpdatingRole] = useState(false);
  const [roleForm] = Form.useForm();
  const [organizationTargetUser, setOrganizationTargetUser] = useState<AdminUser | null>(null);
  const [updatingOrganization, setUpdatingOrganization] = useState(false);
  const [organizationForm] = Form.useForm();
  const [loginRecordsOpen, setLoginRecordsOpen] = useState(false);
  const [loginRecordsUser, setLoginRecordsUser] = useState<AdminUser | null>(null);
  const [loginRecords, setLoginRecords] = useState<AdminLoginRecord[]>([]);
  const [loginRecordsTotal, setLoginRecordsTotal] = useState(0);
  const [loginRecordsPage, setLoginRecordsPage] = useState(1);
  const [loginRecordsLoading, setLoginRecordsLoading] = useState(false);
  const [updatingStatusId, setUpdatingStatusId] = useState<number | null>(null);
  const [updatingUnlockLoginId, setUpdatingUnlockLoginId] = useState<number | null>(null);

  let currentUser: { role?: string; id?: number; organization_id?: number } | null = null;
  try {
    const raw = localStorage.getItem('user_info');
    if (raw) currentUser = JSON.parse(raw);
  } catch {
    // Invalid stored user information grants no administrative capabilities.
  }

  const isSuperAdmin = currentUser?.role === 'super_admin';

  const {
    data: organizations = [],
    isLoading: organizationsLoading,
    isError: organizationsError,
  } = useQuery<OrganizationOption[]>({
    queryKey: ['admin-user-organization-options', currentUser?.role, currentUser?.organization_id],
    enabled: modalOpen || roleModalOpen || organizationTargetUser !== null,
    queryFn: async () => {
      const first = await apiClient.get('/admin/organizations', {
        params: { page: 1, page_size: 100, status: 'active' },
      });
      const data = first.data?.data || { items: [], total: 0 };
      const items: OrganizationOption[] = data.items || [];
      for (let page = 2; items.length < Number(data.total || 0); page++) {
        const response = await apiClient.get('/admin/organizations', {
          params: { page, page_size: 100, status: 'active' },
        });
        const next = response.data?.data?.items || [];
        if (!next.length) break;
        items.push(...next);
      }
      if (currentUser?.role === 'org_admin')
        return items.filter((org: OrganizationOption) => org.id === currentUser.organization_id);
      return items;
    },
  });

  const [form] = Form.useForm();
  const selectedCreateRole = Form.useWatch('role', form);
  const selectedNewRole = Form.useWatch('role', roleForm);

  const fetchAdminUsers = async (p = page, ps = pageSize, query = searchQuery) => {
    const request = ++usersRequest.current;
    setLoading(true);
    setLoadError(false);
    try {
      const params: Record<string, any> = {
        page: p,
        page_size: ps,
      };
      if (roleFilter) params.role = roleFilter;
      if (statusFilter) params.status = statusFilter;
      if (query.trim()) params.search = query.trim();

      const res = await client.get('/admin/admin-users', { params });
      if (request !== usersRequest.current) return;
      const data = res.data?.data;
      setUsers(data?.items || []);
      setTotal(data?.total || 0);
      setPage(p);
      setPageSize(ps);
    } catch (err: any) {
      if (request !== usersRequest.current) return;
      setLoadError(true);
      message.error(
        err?.userMessage || t('adminusers.msg_load_error', 'Failed to load administrative users'),
      );
    } finally {
      if (request === usersRequest.current) setLoading(false);
    }
  };

  useEffect(() => {
    fetchAdminUsers(1, pageSize);
    return () => {
      usersRequest.current++;
    };
  }, [roleFilter, statusFilter]);

  const handleRequestOTP = async () => {
    if (pendingActions.current.otp || pendingActions.current.create) return;
    pendingActions.current.otp = true;
    setOtpError(false);
    try {
      const { email } = await form.validateFields(['email']);
      setSendingOtp(true);
      const res = await client.post(
        '/admin/super-admins/otp',
        { email: email.trim() },
        { skipGlobalErrorMessage: true },
      );
      const expiresIn = res.data?.data?.expires_in || 300;
      setOtpSent(true);
      message.success(
        t('adminusers.msg_otp_sent', '6-digit OTP dispatched (valid for {seconds}s)').replace(
          '{seconds}',
          String(expiresIn),
        ),
      );
    } catch (err: any) {
      if (err?.errorFields) return;
      setOtpError(true);
    } finally {
      pendingActions.current.otp = false;
      setSendingOtp(false);
    }
  };

  const generateRandomPassword = async () => {
    if (pendingActions.current.create || pendingActions.current.otp) return;
    const pwd = generatePassword();

    form.setFieldValue('password', pwd);
    if (await copyText(pwd)) {
      message.success(
        t(
          'adminusers.msg_pwd_generated',
          'Generated 16-character random password and copied to clipboard!',
        ),
      );
    } else {
      message.warning(t('adminusers.msg_pwd_generated_not_copied'));
    }
  };

  const handleCreateAdmin = async (values: {
    email: string;
    password: string;
    otp?: string;
    organization_id: number;
    role: 'super_admin' | 'org_admin';
  }) => {
    if (pendingActions.current.create || pendingActions.current.otp) return;
    const doCreate = async () => {
      if (pendingActions.current.create || pendingActions.current.otp) return;
      pendingActions.current.create = true;
      setCreating(true);
      try {
        await client.post('/admin/super-admins', {
          email: values.email.trim(),
          password: values.password,
          otp: values.otp?.trim() || undefined,
          organization_id: values.role === 'org_admin' ? Number(values.organization_id) : undefined,
          role: values.role,
        });
        message.success(
          t('adminusers.msg_admin_created', 'Administrator {email} created successfully').replace(
            '{email}',
            values.email,
          ),
        );
        setModalOpen(false);
        setOtpSent(false);
        form.resetFields();
        fetchAdminUsers(1, pageSize);
      } catch (err: any) {
        message.error(
          err?.userMessage || t('adminusers.msg_create_error', 'Failed to create administrator'),
        );
      } finally {
        pendingActions.current.create = false;
        setCreating(false);
      }
    };

    if (values.role === 'super_admin') {
      Modal.confirm({
        title: t('adminusers.confirm_super_admin_title', 'Confirm Super Admin Creation'),
        content: t(
          'adminusers.confirm_super_admin_content',
          'super_admin is not constrained by organizations, please create with caution.',
        ),
        okText: t('common.confirm', 'Confirm'),
        cancelText: t('common.cancel', 'Cancel'),
        okButtonProps: { danger: true },
        onOk: async () => {
          await doCreate();
        },
      });
      return;
    }

    await doCreate();
  };

  const handleUpdateRole = async (values: { role: string; organization_id?: number }) => {
    if (!roleTargetUser || pendingActions.current.role) return;
    const doUpdateRole = async () => {
      if (pendingActions.current.role) return;
      pendingActions.current.role = true;
      setUpdatingRole(true);
      try {
        await client.post(`/admin/admin-users/${roleTargetUser.id}/role`, {
          role: values.role,
          organization_id:
            values.role === 'org_admin'
              ? values.organization_id || roleTargetUser.organization_id
              : undefined,
        });
        message.success(
          t('adminusers.msg_role_updated', 'Role for user {email} successfully updated to {role}.')
            .replace('{email}', roleTargetUser.email)
            .replace('{role}', values.role),
        );
        setRoleModalOpen(false);
        roleForm.resetFields();
        setRoleTargetUser(null);
        fetchAdminUsers(page, pageSize);
      } catch (err: any) {
        message.error(
          err?.userMessage || t('adminusers.msg_change_role_error', 'Failed to change user role'),
        );
      } finally {
        pendingActions.current.role = false;
        setUpdatingRole(false);
      }
    };

    if (values.role === 'super_admin' && roleTargetUser.role !== 'super_admin') {
      Modal.confirm({
        title: t('adminusers.confirm_super_admin_title', 'Confirm Super Admin Creation'),
        content: t(
          'adminusers.confirm_super_admin_content',
          'super_admin is not constrained by organizations, please create with caution.',
        ),
        okText: t('common.confirm', 'Confirm'),
        cancelText: t('common.cancel', 'Cancel'),
        okButtonProps: { danger: true },
        onOk: async () => {
          await doUpdateRole();
        },
      });
      return;
    }

    await doUpdateRole();
  };

  const handleUpdateStatus = async (record: AdminUser, newStatus: 'active' | 'disabled') => {
    setUpdatingStatusId(record.id);
    try {
      await client.post(`/admin/admin-users/${record.id}/status`, { status: newStatus });
      const successMsg =
        newStatus === 'disabled'
          ? t(
              'adminusers.msg_status_disabled_success',
              'Administrator {email} has been disabled.',
            ).replace('{email}', record.email)
          : t(
              'adminusers.msg_status_enabled_success',
              'Administrator {email} has been enabled.',
            ).replace('{email}', record.email);
      message.success(successMsg);
      fetchAdminUsers(page, pageSize);
    } catch (err: any) {
      message.error(
        err?.userMessage || t('adminusers.msg_status_update_error', 'Failed to update user status'),
      );
    } finally {
      setUpdatingStatusId(null);
    }
  };

  const handleUpdateOrganization = async (values: { organization_id: number }) => {
    if (!organizationTargetUser) return;
    setUpdatingOrganization(true);
    try {
      await client.put(`/admin/admin-users/${organizationTargetUser.id}/organization`, values);
      message.success(
        t(
          'adminusers.msg_organization_updated',
          'Organization for {email} updated successfully.',
        ).replace('{email}', organizationTargetUser.email),
      );
      setOrganizationTargetUser(null);
      organizationForm.resetFields();
      await fetchAdminUsers(page, pageSize);
    } catch (err: any) {
      message.error(
        err?.userMessage ||
          t('adminusers.msg_change_organization_error', 'Failed to change user organization'),
      );
    } finally {
      setUpdatingOrganization(false);
    }
  };

  const generateResetPassword = async () => {
    if (pendingActions.current.resetPassword) return;
    const pwd = generatePassword();

    resetForm.setFieldValue('new_password', pwd);
    if (await copyText(pwd)) {
      message.success(
        t(
          'adminusers.msg_pwd_generated',
          'Generated 16-character random password and copied to clipboard!',
        ),
      );
    } else {
      message.warning(t('adminusers.msg_pwd_generated_not_copied'));
    }
  };

  const handleAdminResetPassword = async (values: { new_password: string }) => {
    if (!selectedUser || pendingActions.current.resetPassword) return;
    pendingActions.current.resetPassword = true;
    setResettingPassword(true);
    try {
      await client.post(`/admin/admin-users/${selectedUser.id}/reset-password`, {
        new_password: values.new_password,
      });
      message.success(
        t(
          'adminusers.msg_pwd_reset_success',
          'Password for user {email} reset successfully.',
        ).replace('{email}', selectedUser.email),
      );
      setResetModalOpen(false);
      resetForm.resetFields();
      setSelectedUser(null);
    } catch (err: any) {
      message.error(
        err?.userMessage || t('adminusers.msg_reset_pwd_error', 'Failed to reset password'),
      );
    } finally {
      pendingActions.current.resetPassword = false;
      setResettingPassword(false);
    }
  };

  const handleResetMFA = (record: AdminUser) => {
    Modal.confirm({
      title: t('adminusers.confirm_mfa_title', 'Reset Two-Factor Authentication'),
      content: t(
        'adminusers.confirm_mfa_content',
        'Are you sure you want to disable and reset MFA for administrator {email}? All active sessions will also be invalidated.',
      ).replace('{email}', record.email),
      okText: t('adminusers.confirm_mfa_ok', 'Reset MFA'),
      okButtonProps: { danger: true },
      cancelText: t('adminusers.confirm_mfa_cancel', 'Cancel'),
      onOk: async () => {
        try {
          await client.post(`/admin/admin-users/${record.id}/reset-mfa`);
          message.success(
            t(
              'adminusers.msg_mfa_reset_success',
              'Two-factor authentication for {email} has been reset.',
            ).replace('{email}', record.email),
          );
          fetchAdminUsers(page, pageSize);
        } catch (err: any) {
          message.error(
            err?.userMessage || t('adminusers.msg_reset_mfa_error', 'Failed to reset MFA'),
          );
          throw err;
        }
      },
    });
  };

  const handleUnlockLogin = (record: AdminUser) => {
    Modal.confirm({
      title: t('adminusers.confirm_unlock_login_title', 'Unlock Login'),
      content: t(
        'adminusers.confirm_unlock_login_content',
        'Remove the temporary login lock for {email}?',
      ).replace('{email}', record.email),
      okText: t('adminusers.btn_unlock_login', 'Unlock Login'),
      cancelText: t('common.cancel', 'Cancel'),
      onOk: async () => {
        setUpdatingUnlockLoginId(record.id);
        try {
          await client.post(`/admin/admin-users/${record.id}/unlock-login`);
          message.success(
            t('adminusers.msg_unlock_login_success', 'Login lock removed for {email}.').replace(
              '{email}',
              record.email,
            ),
          );
        } catch (err: any) {
          message.error(
            err?.userMessage ||
              t('adminusers.msg_unlock_login_error', 'Failed to remove login lock'),
          );
          throw err;
        } finally {
          setUpdatingUnlockLoginId(null);
        }
      },
    });
  };

  const fetchLoginRecords = async (record: AdminUser, p = 1) => {
    setLoginRecordsUser(record);
    setLoginRecordsLoading(true);
    try {
      const response = await client.get(`/admin/admin-users/${record.id}/login-records`, {
        params: { page: p, page_size: 20 },
      });
      const data = response.data?.data || {};
      setLoginRecords(data.items || []);
      setLoginRecordsTotal(data.total || 0);
      setLoginRecordsPage(p);
      setLoginRecordsOpen(true);
    } catch (err: any) {
      message.error(
        err?.userMessage || t('adminusers.msg_login_records_error', 'Failed to load login records'),
      );
    } finally {
      setLoginRecordsLoading(false);
    }
  };

  const actionColumnWidth = language === 'zh-CN' ? 230 : 285;
  const columns = [
    {
      title: t('adminusers.col_email', 'Email'),
      dataIndex: 'email',
      key: 'email',
      width: 220,
      render: (email: string, record: AdminUser) =>
        isSuperAdmin ? (
          <Button
            type="link"
            className="console-record-title"
            loading={loginRecordsLoading && loginRecordsUser?.id === record.id}
            onMouseEnter={() => {
              void loadLoginRecordsModal();
            }}
            onFocus={() => {
              void loadLoginRecordsModal();
            }}
            onClick={() => {
              void loadLoginRecordsModal();
              void fetchLoginRecords(record);
            }}
            style={{ padding: 0, fontWeight: 600 }}
          >
            {email}
          </Button>
        ) : (
          <span className="console-record-title">{email}</span>
        ),
    },
    {
      title: t('adminusers.col_role', 'Role'),
      dataIndex: 'role',
      key: 'role',
      width: 120,
      render: (role: string) => <span className="console-chip">{role?.toUpperCase()}</span>,
    },
    {
      title: t('adminusers.col_org', 'Organization ID'),
      dataIndex: 'organization_id',
      key: 'organization_id',
      width: 140,
      render: (orgId: number, record: AdminUser) =>
        record.role === 'super_admin' ? (
          <Text type="secondary">{t('adminusers.no_organization')}</Text>
        ) : (
          <ResourceName kind="organizations" id={orgId} />
        ),
    },
    {
      title: t('adminusers.col_mfa', 'MFA'),
      dataIndex: 'totp_enabled',
      key: 'totp_enabled',
      width: 150,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (enabled?: boolean) => (
        <Tag color={enabled ? 'green' : 'default'} style={{ whiteSpace: 'nowrap' }}>
          {enabled
            ? t('adminusers.mfa_enabled', 'ENABLED')
            : t('adminusers.mfa_disabled', 'DISABLED')}
        </Tag>
      ),
    },
    {
      title: t('adminusers.col_status', 'Status'),
      dataIndex: 'status',
      key: 'status',
      width: 160,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (status: string, record: AdminUser) => (
        <Space size={4} wrap={false}>
          <StatusTag value={status} />
          {record.login_locked && (
            <Tooltip
              title={
                record.login_locked_until
                  ? formatDateTime(record.login_locked_until)
                  : t('adminusers.login_locked_until', 'Locked until')
              }
            >
              <Tag color="error" style={{ whiteSpace: 'nowrap' }}>
                {t('adminusers.status_login_locked', 'Login Locked')}
              </Tag>
            </Tooltip>
          )}
        </Space>
      ),
    },
    {
      title: `${t('adminusers.col_created_at', 'Created At')} (${timezoneBadge})`,
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (d: string) =>
        d ? (
          <span style={{ whiteSpace: 'nowrap', display: 'inline-block' }}>{formatDateTime(d)}</span>
        ) : (
          '-'
        ),
    },
    {
      title: t('adminusers.col_action', 'Action'),
      key: 'action',
      width: actionColumnWidth,
      onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (_: any, record: AdminUser) => {
        const isSelf = currentUser?.id != null && record.id === currentUser.id;
        const cannotChangeRole = isSelf || !isSuperAdmin;

        return (
          <Space size={2} wrap={false} style={{ whiteSpace: 'nowrap' }}>
            {isSuperAdmin && record.role === 'org_admin' && (
              <Tooltip
                title={
                  isSelf
                    ? t(
                        'adminusers.cannot_change_own_organization',
                        'Cannot change your own organization',
                      )
                    : t('adminusers.btn_change_organization', 'Change Organization')
                }
              >
                <span>
                  <TableAction
                    size="small"
                    aria-label={t('adminusers.btn_change_organization', 'Change Organization')}
                    disabled={isSelf}
                    onClick={() => {
                      organizationForm.resetFields();
                      setOrganizationTargetUser(record);
                    }}
                  >
                    {t('adminusers.btn_change_organization', 'Change Organization')}
                  </TableAction>
                </span>
              </Tooltip>
            )}
            <Tooltip
              title={
                isSelf
                  ? t('adminusers.cannot_change_own_role', 'Cannot change your own role')
                  : undefined
              }
            >
              <span>
                <TableAction
                  size="small"
                  disabled={cannotChangeRole}
                  onClick={() => {
                    setRoleTargetUser(record);
                    roleForm.setFieldsValue({ role: record.role });
                    setRoleModalOpen(true);
                  }}
                >
                  {t('adminusers.btn_change_role', 'Change Role')}
                </TableAction>
              </span>
            </Tooltip>
            {isSuperAdmin && (
              <TableActionMenu
                loading={updatingStatusId === record.id || updatingUnlockLoginId === record.id}
                menu={{
                  items: [
                    {
                      key: 'password',
                      label: t('adminusers.btn_reset_pwd', 'Reset Password'),
                      onClick: () => {
                        setSelectedUser(record);
                        resetForm.resetFields();
                        setResetModalOpen(true);
                      },
                    },
                    ...(record.totp_enabled
                      ? [
                          {
                            key: 'mfa',
                            label: t('adminusers.btn_reset_mfa', 'Reset MFA'),
                            danger: true,
                            onClick: () => handleResetMFA(record),
                          },
                        ]
                      : []),
                    ...(record.login_locked
                      ? [
                          {
                            key: 'unlock-login',
                            label: t('adminusers.btn_unlock_login', 'Unlock Login'),
                            disabled: updatingUnlockLoginId === record.id,
                            onClick: () => handleUnlockLogin(record),
                          },
                        ]
                      : []),
                    { type: 'divider' },
                    {
                      key: 'status',
                      label: t(
                        record.status === 'active'
                          ? 'adminusers.btn_disable'
                          : 'adminusers.btn_enable',
                      ),
                      danger: record.status === 'active',
                      disabled:
                        updatingStatusId === record.id || (record.status === 'active' && isSelf),
                      onClick: () =>
                        Modal.confirm({
                          title:
                            record.status === 'active'
                              ? t(
                                  'adminusers.confirm_disable_title',
                                  'Disable Administrator Account',
                                )
                              : t(
                                  'adminusers.confirm_enable_title',
                                  'Enable Administrator Account',
                                ),
                          content: (record.status === 'active'
                            ? t(
                                'adminusers.confirm_disable_content',
                                'Are you sure you want to disable administrator {email}? The user will be immediately logged out and unable to sign in.',
                              )
                            : t(
                                'adminusers.confirm_enable_content',
                                'Are you sure you want to enable administrator {email}?',
                              )
                          ).replace('{email}', record.email),
                          okText: t(
                            record.status === 'active'
                              ? 'adminusers.btn_disable'
                              : 'adminusers.btn_enable',
                          ),
                          cancelText: t('common.cancel'),
                          okButtonProps: { danger: record.status === 'active' },
                          onOk: () =>
                            handleUpdateStatus(
                              record,
                              record.status === 'active' ? 'disabled' : 'active',
                            ),
                        }),
                    },
                  ],
                }}
              />
            )}
          </Space>
        );
      },
    },
  ];

  const changePage = (nextPage: number, nextPageSize = pageSize) => {
    const boundedPage = Math.max(
      1,
      Math.min(nextPage, Math.max(1, Math.ceil(total / nextPageSize))),
    );
    void fetchAdminUsers(boundedPage, nextPageSize);
  };

  return (
    <>
      <Card>
        <ConsoleToolbar
          actions={
            <>
              <Tooltip
                title={
                  !isSuperAdmin
                    ? t(
                        'adminusers.provision_super_admin_only_tip',
                        'Provisioning administrators requires super administrator permissions',
                      )
                    : undefined
                }
              >
                <span>
                  <Button
                    type="primary"
                    icon={<PlusOutlined />}
                    disabled={!isSuperAdmin}
                    onClick={() => {
                      form.resetFields();
                      form.setFieldsValue({
                        role: 'org_admin',
                        organization_id:
                          currentUser?.role === 'org_admin'
                            ? currentUser.organization_id
                            : undefined,
                      });
                      setOtpSent(false);
                      setOtpError(false);
                      setModalOpen(true);
                    }}
                  >
                    {t('adminusers.btn_provision', 'Provision Admin')}
                  </Button>
                </span>
              </Tooltip>
            </>
          }
        >
          <SearchInput
            placeholder={t('adminusers.search_placeholder', 'Search by email...')}
            allowClear
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            onSearch={value => fetchAdminUsers(1, pageSize, value)}
            style={{ width: 200 }}
          />
          <Select
            placeholder={t('adminusers.filter_role', 'Filter by Role')}
            aria-label={t('adminusers.filter_role', 'Filter by Role')}
            allowClear
            style={{ width: 130 }}
            value={roleFilter || undefined}
            onChange={v => setRoleFilter(v || '')}
            options={[
              { value: 'super_admin', label: 'super_admin' },
              { value: 'org_admin', label: 'org_admin' },
            ]}
          />
          <Select
            placeholder={t('adminusers.filter_status', 'Filter by Status')}
            aria-label={t('adminusers.filter_status', 'Filter by Status')}
            allowClear
            style={{ width: 120 }}
            value={statusFilter || undefined}
            onChange={v => setStatusFilter(v || '')}
            options={[
              { value: 'active', label: t('adminusers.status_active', 'Active') },
              { value: 'disabled', label: t('adminusers.status_disabled', 'Disabled') },
            ]}
          />
          <RefreshButton onClick={() => fetchAdminUsers(page, pageSize)} loading={loading}>
            {t('adminusers.btn_refresh', 'Refresh')}
          </RefreshButton>
        </ConsoleToolbar>
        {loadError && (
          <LoadError
            retry={() => {
              fetchAdminUsers(page, pageSize);
            }}
          />
        )}
        <div className="admin-users-table-shell" aria-busy={loading}>
          {loading && (
            <div className="admin-users-table-loading" role="status">
              {t('common.loading', 'Loading...')}
            </div>
          )}
          <div className="admin-users-table-scroll">
            <table className="admin-users-data-table">
              <colgroup>
                {columns.map(column => (
                  <col key={column.key} style={{ width: column.width }} />
                ))}
              </colgroup>
              <thead>
                <tr>
                  {columns.map(column => (
                    <th
                      key={column.key}
                      className={column.key === 'action' ? 'admin-users-action-cell' : undefined}
                    >
                      {column.title}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {!loading && users.length === 0 ? (
                  <tr>
                    <td colSpan={columns.length} className="admin-users-empty">
                      {t('common.none', 'None')}
                    </td>
                  </tr>
                ) : (
                  users.map(record => (
                    <tr key={record.id}>
                      {columns.map(rawColumn => {
                        const column = rawColumn as any;
                        const value = column.dataIndex
                          ? (record as any)[column.dataIndex]
                          : undefined;
                        return (
                          <td
                            key={column.key}
                            className={
                              column.key === 'action' ? 'admin-users-action-cell' : undefined
                            }
                          >
                            {column.render ? column.render(value, record) : String(value ?? '-')}
                          </td>
                        );
                      })}
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
          <div className="admin-users-pagination">
            <Pagination
              current={page}
              pageSize={pageSize}
              total={total}
              disabled={loading}
              showSizeChanger
              pageSizeOptions={['10', '20', '50', '100']}
              showTotal={count => `${t('common.total', 'Total')} ${count}`}
              onChange={changePage}
            />
          </div>
        </div>
      </Card>

      <Modal
        title={
          <div style={{ paddingRight: 40, lineHeight: 1.4 }}>
            {t('adminusers.modal_provision_title', 'Provision Administrator Account')}
          </div>
        }
        open={modalOpen}
        maskClosable={false}
        closable={!creating && !sendingOtp}
        keyboard={!creating && !sendingOtp}
        onCancel={() => {
          if (!pendingActions.current.create && !pendingActions.current.otp) setModalOpen(false);
        }}
        footer={null}
        destroyOnHidden
        width={560}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t(
            'adminusers.modal_provision_alert',
            'Requires super administrator permissions. Verification codes are optional.',
          )}
        />
        <Form
          form={form}
          layout="vertical"
          disabled={creating || sendingOtp}
          onFinish={handleCreateAdmin}
        >
          <Form.Item
            name="email"
            label={t('adminusers.label_email', 'Target Administrator Email')}
            rules={[
              { required: true, message: t('adminusers.email_required', 'Email is required') },
              {
                type: 'email',
                message: t('adminusers.email_invalid', 'Enter a valid email address'),
              },
            ]}
          >
            <Input
              prefix={<MailOutlined />}
              placeholder="newadmin@bosapi.net"
              onChange={() => {
                setOtpSent(false);
                setOtpError(false);
                form.setFieldValue('otp', undefined);
              }}
            />
          </Form.Item>

          <Form.Item
            name="otp"
            label={t('adminusers.label_otp', '6-Digit Verification Code (Optional)')}
            extra={t('adminusers.otp_recovery_help')}
            rules={[
              { pattern: /^\d{6}$/, message: t('adminusers.otp_len', 'Enter exactly 6 digits') },
            ]}
          >
            <Input
              prefix={<SafetyCertificateOutlined />}
              placeholder="123456"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              addonAfter={
                <Button
                  type="link"
                  size="small"
                  loading={sendingOtp}
                  onClick={handleRequestOTP}
                  style={{ padding: 0 }}
                >
                  {otpSent
                    ? t('adminusers.btn_resend_otp', 'Resend OTP')
                    : t('adminusers.btn_send_otp', 'Send OTP')}
                </Button>
              }
            />
          </Form.Item>

          {otpError && (
            <Alert
              type="error"
              showIcon
              role="alert"
              style={{ marginBottom: 16 }}
              message={t('adminusers.msg_otp_error')}
            />
          )}

          <div style={{ position: 'relative' }}>
            <Form.Item
              name="password"
              label={t('adminusers.label_initial_pwd', 'Initial Password (minimum 12 characters)')}
              rules={[
                { required: true, message: t('adminusers.pwd_required', 'Password is required') },
                {
                  min: 12,
                  message: t('adminusers.pwd_min', 'Password must be at least 12 characters'),
                },
              ]}
              style={{ marginBottom: 4 }}
            >
              <Input.Password placeholder="At least 12 characters" />
            </Form.Item>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 14 }}>
              <Button
                type="link"
                size="small"
                icon={<KeyOutlined />}
                disabled={creating || sendingOtp}
                onClick={generateRandomPassword}
                style={{ padding: 0, fontSize: 13 }}
              >
                {t('adminusers.btn_generate_pwd', 'Generate Random Password')}
              </Button>
            </div>
          </div>

          <Row gutter={16}>
            <Col span={14}>
              <Form.Item
                name="role"
                label={t('adminusers.label_role', 'Assigned Role')}
                rules={[{ required: true }]}
              >
                <Select
                  style={{ width: '100%' }}
                  options={[
                    ...(isSuperAdmin
                      ? [
                          {
                            value: 'super_admin',
                            label: t(
                              'adminusers.role_super_admin_option',
                              'Super Admin (super_admin)',
                            ),
                          },
                        ]
                      : []),
                    {
                      value: 'org_admin',
                      label: t(
                        'adminusers.role_org_admin_option',
                        'Organization Admin (org_admin)',
                      ),
                    },
                  ]}
                />
              </Form.Item>
            </Col>

            <Col span={10}>
              <Form.Item
                name="organization_id"
                hidden={selectedCreateRole === 'super_admin'}
                label={t('adminusers.label_org_id', 'Organization')}
                rules={[
                  {
                    required: selectedCreateRole !== 'super_admin',
                    message: t('adminusers.org_id_required', 'Required'),
                  },
                ]}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  loading={organizationsLoading}
                  placeholder={t('adminusers.select_org', 'Select an organization')}
                  options={organizations.map(organization => ({
                    value: organization.id,
                    label: organization.name,
                  }))}
                />
              </Form.Item>
            </Col>
          </Row>

          {selectedCreateRole === 'super_admin' && (
            <Alert
              type="warning"
              showIcon
              message={t(
                'adminusers.confirm_super_admin_content',
                'super_admin is not constrained by organizations, please create with caution.',
              )}
              style={{ marginBottom: 16 }}
            />
          )}

          <Form.Item style={{ marginBottom: 0, textAlign: 'right' }}>
            <Space>
              <Button
                disabled={creating || sendingOtp}
                onClick={() => {
                  if (!pendingActions.current.create && !pendingActions.current.otp)
                    setModalOpen(false);
                }}
              >
                {t('common.cancel', 'Cancel')}
              </Button>
              <Button type="primary" htmlType="submit" loading={creating}>
                {t('adminusers.btn_create_admin', 'Create Administrator')}
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {loginRecordsOpen && (
        <Suspense fallback={null}>
          <AdminLoginRecordsModal
            open
            userEmail={loginRecordsUser?.email}
            records={loginRecords}
            loading={loginRecordsLoading}
            page={loginRecordsPage}
            total={loginRecordsTotal}
            onClose={() => setLoginRecordsOpen(false)}
            onPageChange={nextPage =>
              loginRecordsUser && fetchLoginRecords(loginRecordsUser, nextPage)
            }
          />
        </Suspense>
      )}

      {/* Admin Reset User Password Modal */}
      <Modal
        title={
          <div style={{ fontWeight: 600, fontSize: 16, color: '#171717' }}>
            {t('adminusers.modal_reset_pwd_title', 'Reset User Password')}
          </div>
        }
        open={resetModalOpen}
        closable={!resettingPassword}
        maskClosable={!resettingPassword}
        keyboard={!resettingPassword}
        onCancel={() => {
          if (pendingActions.current.resetPassword) return;
          setResetModalOpen(false);
          resetForm.resetFields();
          setSelectedUser(null);
        }}
        footer={null}
        width={450}
        destroyOnHidden
      >
        {selectedUser && (
          <div
            style={{
              padding: '12px 14px',
              borderRadius: 8,
              background: '#fafafa',
              border: '1px solid #f0f0f0',
              marginBottom: 20,
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <Text strong style={{ fontSize: 14, color: '#171717' }}>
                  {selectedUser.email}
                </Text>
                <div style={{ color: '#737373', fontSize: 12, marginTop: 2 }}>
                  {selectedUser.role === 'super_admin' ? (
                    t('adminusers.no_organization')
                  ) : (
                    <ResourceName kind="organizations" id={selectedUser.organization_id} />
                  )}
                </div>
              </div>
              <Tag
                color={
                  selectedUser.role === 'super_admin'
                    ? 'purple'
                    : selectedUser.role === 'org_admin'
                      ? 'cyan'
                      : 'default'
                }
              >
                {selectedUser.role.toUpperCase()}
              </Tag>
            </div>
          </div>
        )}

        <Form
          form={resetForm}
          layout="vertical"
          disabled={resettingPassword}
          onFinish={handleAdminResetPassword}
        >
          <div style={{ position: 'relative' }}>
            <Form.Item
              name="new_password"
              label={t('adminusers.label_new_pwd', 'New Password')}
              rules={[
                { required: true, message: t('auth.enter_new_pwd', 'Please enter a new password') },
                {
                  min: 12,
                  message: t('auth.pwd_min_length', 'Password must be at least 12 characters'),
                },
              ]}
              extra={t(
                'adminusers.reset_pwd_help',
                'Password must be at least 12 characters. All active sessions for this user will be revoked immediately.',
              )}
            >
              <Input.Password
                placeholder={t('adminusers.placeholder_new_pwd', 'Minimum 12 characters')}
                size="large"
              />
            </Form.Item>
            <div style={{ position: 'absolute', top: 0, right: 0 }}>
              <Button
                type="link"
                size="small"
                disabled={resettingPassword}
                onClick={generateResetPassword}
                style={{ padding: 0, fontSize: 13 }}
              >
                {t('adminusers.btn_generate_pwd', 'Generate Random Password')}
              </Button>
            </div>
          </div>

          <Form.Item style={{ marginTop: 24, marginBottom: 0, textAlign: 'right' }}>
            <Space>
              <Button
                disabled={resettingPassword}
                onClick={() => {
                  if (!pendingActions.current.resetPassword) setResetModalOpen(false);
                }}
              >
                {t('common.cancel', 'Cancel')}
              </Button>
              <Button type="primary" htmlType="submit" loading={resettingPassword}>
                {t('adminusers.btn_confirm_reset', 'Confirm Reset')}
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t('adminusers.btn_change_organization', 'Change Organization')}
        open={organizationTargetUser !== null}
        onCancel={() => {
          if (updatingOrganization) return;
          setOrganizationTargetUser(null);
          organizationForm.resetFields();
        }}
        closable={!updatingOrganization}
        maskClosable={!updatingOrganization}
        keyboard={!updatingOrganization}
        footer={null}
        width={480}
        destroyOnHidden
      >
        <p style={{ overflowWrap: 'anywhere' }}>
          <Text strong>{organizationTargetUser?.email}</Text>
          <br />
          <Text type="secondary">
            <ResourceName kind="organizations" id={organizationTargetUser?.organization_id} />
          </Text>
        </p>
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 20 }}
          message={t(
            'adminusers.change_organization_help',
            'Existing non-deleted API keys will move with the user and follow the target organization policies. Historical usage stays with its original organization. The user must sign in again.',
          )}
        />
        {organizationsError && (
          <Alert
            type="error"
            showIcon
            style={{ marginBottom: 16 }}
            message={t('adminusers.msg_organizations_load_error', 'Failed to load organizations')}
          />
        )}
        <Form form={organizationForm} layout="vertical" onFinish={handleUpdateOrganization}>
          <Form.Item
            name="organization_id"
            label={t('adminusers.label_target_organization', 'Target Organization')}
            rules={[
              { required: true, message: t('adminusers.select_org', 'Select an organization') },
              {
                validator: (_, value) =>
                  !value ||
                  organizations.some(
                    organization =>
                      organization.id === value &&
                      organization.status === 'active' &&
                      value !== organizationTargetUser?.organization_id,
                  )
                    ? Promise.resolve()
                    : Promise.reject(
                        new Error(t('adminusers.select_org', 'Select an organization')),
                      ),
              },
            ]}
          >
            <Select
              showSearch
              optionFilterProp="label"
              loading={organizationsLoading}
              disabled={updatingOrganization || organizationsError}
              placeholder={t('adminusers.select_org', 'Select an organization')}
              options={organizations
                .filter(
                  organization =>
                    organization.status === 'active' &&
                    organization.id !== organizationTargetUser?.organization_id,
                )
                .map(organization => ({ value: organization.id, label: organization.name }))}
            />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0, textAlign: 'right' }}>
            <Space>
              <Button
                disabled={updatingOrganization}
                onClick={() => {
                  setOrganizationTargetUser(null);
                  organizationForm.resetFields();
                }}
              >
                {t('common.cancel', 'Cancel')}
              </Button>
              <Button
                type="primary"
                htmlType="submit"
                loading={updatingOrganization}
                disabled={organizationsLoading || organizationsError}
              >
                {t('common.confirm', 'Confirm')}
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {/* Change User Role Modal */}
      <Modal
        title={
          <div style={{ fontWeight: 600, fontSize: 16, color: '#171717' }}>
            <UserSwitchOutlined style={{ marginRight: 8, color: 'var(--ink)' }} />
            {t('adminusers.modal_change_role_title', 'Change User Management Role')}
          </div>
        }
        open={roleModalOpen}
        closable={!updatingRole}
        maskClosable={!updatingRole}
        keyboard={!updatingRole}
        onCancel={() => {
          if (pendingActions.current.role) return;
          setRoleModalOpen(false);
          roleForm.resetFields();
          setRoleTargetUser(null);
        }}
        footer={null}
        width={480}
        destroyOnHidden
      >
        {roleTargetUser && (
          <div
            style={{
              padding: '12px 14px',
              borderRadius: 8,
              background: '#fafafa',
              border: '1px solid #f0f0f0',
              marginBottom: 16,
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <Text strong style={{ fontSize: 14, color: '#171717' }}>
                  {roleTargetUser.email}
                </Text>
                <div style={{ color: '#737373', fontSize: 12, marginTop: 2 }}>
                  {roleTargetUser.role === 'super_admin' ? (
                    t('adminusers.no_organization')
                  ) : (
                    <ResourceName kind="organizations" id={roleTargetUser.organization_id} />
                  )}
                </div>
              </div>
              <div>
                <span style={{ fontSize: 12, color: '#8c8c8c', marginRight: 6 }}>
                  {t('adminusers.label_current_role', 'Current Role')}:
                </span>
                <Tag
                  color={
                    roleTargetUser.role === 'super_admin'
                      ? 'purple'
                      : roleTargetUser.role === 'org_admin'
                        ? 'cyan'
                        : 'default'
                  }
                >
                  {roleTargetUser.role.toUpperCase()}
                </Tag>
              </div>
            </div>
          </div>
        )}

        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 20 }}
          message={t(
            'adminusers.change_role_help',
            "After changing the role, the user's active sessions and tokens will be invalidated immediately. They must re-login to apply new permissions.",
          )}
        />

        <Form form={roleForm} layout="vertical" disabled={updatingRole} onFinish={handleUpdateRole}>
          {selectedNewRole === 'org_admin' && !roleTargetUser?.organization_id && (
            <Form.Item
              name="organization_id"
              label={t('adminusers.label_org_id')}
              rules={[{ required: true, message: t('adminusers.org_id_required') }]}
            >
              <Select
                loading={organizationsLoading}
                options={organizations.map(organization => ({
                  value: organization.id,
                  label: organization.name,
                }))}
              />
            </Form.Item>
          )}
          <Form.Item
            name="role"
            label={t('adminusers.label_new_role', 'Target Management Role')}
            rules={[
              { required: true, message: t('adminusers.filter_role', 'Please select a role') },
            ]}
          >
            <Select
              style={{ width: '100%' }}
              size="large"
              showSearch={false}
              menuItemSelectedIcon={<CheckOutlined style={{ color: '#171717', fontSize: 13 }} />}
              options={[
                ...(isSuperAdmin
                  ? [
                      {
                        value: 'super_admin',
                        label: (
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                            <Tag color="purple">SUPER_ADMIN</Tag>
                            <span style={{ color: '#171717', fontWeight: 500 }}>
                              {t('adminusers.role_super_admin_option', 'Super Admin (super_admin)')}
                            </span>
                          </div>
                        ),
                      },
                    ]
                  : []),
                {
                  value: 'org_admin',
                  label: (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Tag color="cyan">ORG_ADMIN</Tag>
                      <span style={{ color: '#171717', fontWeight: 500 }}>
                        {t('adminusers.role_org_admin_option', 'Organization Admin (org_admin)')}
                      </span>
                    </div>
                  ),
                },
              ]}
            />
          </Form.Item>

          <Form.Item style={{ marginTop: 24, marginBottom: 0, textAlign: 'right' }}>
            <Space>
              <Button
                disabled={updatingRole}
                onClick={() => {
                  if (pendingActions.current.role) return;
                  setRoleModalOpen(false);
                  roleForm.resetFields();
                  setRoleTargetUser(null);
                }}
              >
                {t('common.cancel', 'Cancel')}
              </Button>
              <Button type="primary" htmlType="submit" loading={updatingRole}>
                {t('adminusers.btn_confirm_change_role', 'Confirm Role Change')}
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};

export default AdminUsersPage;
