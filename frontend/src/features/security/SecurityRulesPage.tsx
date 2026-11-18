import { TableAction } from '../common/TableAction';
import { ResourceName } from '../common/ResourceName';
import { RefreshButton } from '../common/RefreshButton';
import React, { useState, useEffect } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
  Drawer,
  Descriptions,
} from 'antd';
import { InfoCircleOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { AdvancedFields, PageHelp, useDiscardChanges } from '../common/ConsoleUX';
import { OrganizationSelect } from '../common/OrganizationSelect';
import { actionKey } from '../common/ux';

interface SecurityRule {
  id?: number;
  organization_id: number;
  name: string;
  pattern: string;
  action: 'observe' | 'block';
  sensitivity: 'normal' | 'high';
  enabled: boolean;
  version?: number;
}

export const SecurityRulesPage: React.FC = () => {
  const { t } = useI18n();
  const client = useQueryClient();
  const [organization, setOrganization] = useState<number>(0);
  const [editing, setEditing] = useState<SecurityRule | null>(null);
  const [saving, setSaving] = useState(false);
  const [savingMode, setSavingMode] = useState(false);
  const [savingInspectionMode, setSavingInspectionMode] = useState(false);
  const [detail, setDetail] = useState<SecurityRule | null>(null);
  const [form] = Form.useForm<SecurityRule>();
  const cancel = useDiscardChanges(form, () => setEditing(null));
  const [isWideScreen, setIsWideScreen] = useState(() => {
    if (typeof window === 'undefined') return true;
    return window.innerWidth >= 1100;
  });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const mql = window.matchMedia('(min-width: 1100px)');
    const handler = (e: MediaQueryListEvent) => setIsWideScreen(e.matches);
    setIsWideScreen(mql.matches);
    mql.addEventListener('change', handler);
    return () => mql.removeEventListener('change', handler);
  }, []);
  const profile = useQuery({
    queryKey: ['security-rule-profile'],
    queryFn: async () => (await apiClient.get('/auth/me')).data.data,
  });
  const isOrgAdmin = profile.data?.role === 'org_admin';
  const isSuperAdmin = profile.data?.role === 'super_admin';
  const canManage = isSuperAdmin || isOrgAdmin || profile.data?.role === 'admin';
  const organizationID = isOrgAdmin ? profile.data.organization_id : organization;
  const rules = useQuery({
    queryKey: ['security-rules', organizationID],
    enabled: !!profile.data,
    queryFn: async (): Promise<SecurityRule[]> =>
      (
        await apiClient.get('/admin/security-rules', {
          params: { organization_id: organizationID },
        })
      ).data.data.items,
  });

  const policy = useQuery({
    queryKey: ['tool-security-policy'],
    enabled: isSuperAdmin,
    queryFn: async (): Promise<{ mode: 'observe' | 'block' }> =>
      (await apiClient.get('/admin/tool-security-policy')).data.data,
  });
  const inspectionPolicy = useQuery({
    queryKey: ['security-inspection-policy'],
    enabled: isSuperAdmin,
    queryFn: async (): Promise<{ mode: 'observe' | 'block' | 'disabled' }> =>
      (await apiClient.get('/admin/security-inspection-policy')).data.data,
  });
  const saveInspectionMode = async (mode: 'observe' | 'block' | 'disabled') => {
    if (!isSuperAdmin || savingInspectionMode) return;
    setSavingInspectionMode(true);
    try {
      const response = await apiClient.put('/admin/security-inspection-policy', { mode });
      client.setQueryData(['security-inspection-policy'], response.data.data);
      message.success(t('security.mode_saved'));
    } catch (error: any) {
      message.error(error?.userMessage || t('security.mode_save_error'));
    } finally {
      setSavingInspectionMode(false);
    }
  };

  const saveMode = async (mode: 'observe' | 'block') => {
    if (!isSuperAdmin || savingMode) return;
    setSavingMode(true);
    try {
      const response = await apiClient.put('/admin/tool-security-policy', { mode });
      client.setQueryData(['tool-security-policy'], response.data.data);
      message.success(t('security.mode_saved'));
    } catch (error: any) {
      message.error(error?.userMessage || t('security.mode_save_error'));
    } finally {
      setSavingMode(false);
    }
  };

  const open = (rule: SecurityRule) => {
    setEditing(rule);
    form.resetFields();
    form.setFieldsValue(rule);
    form.setFields(
      (Object.keys(rule) as (keyof SecurityRule)[]).map(name => ({ name, touched: false })),
    );
  };
  const save = async () => {
    if (saving || !editing || !canManage) return;
    let values: SecurityRule;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSaving(true);
    try {
      const payload = {
        ...values,
        organization_id: editing!.organization_id,
        version: editing?.version,
      };
      if (editing?.id) await apiClient.put(`/admin/security-rules/${editing.id}`, payload);
      else await apiClient.post('/admin/security-rules', payload);
      setEditing(null);
      await client.invalidateQueries({ queryKey: ['security-rules'] });
      message.success(t('security.rules_saved', 'Rule saved. New scans refresh within 5 seconds.'));
    } catch (error: any) {
      message.error(
        error?.userMessage ||
          t(
            'security.rules_save_error',
            'Could not save rule. Refresh if another administrator edited it.',
          ),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card
      title={
        <Space align="center" size={8}>
          <span>{t('security.rules_title', 'Tool Security Rules')}</span>
          <Tooltip
            title={t(
              'security.rules_note',
              'The global baseline always applies. Only super administrators can edit it. Organization rules are additive. Block removes only the matched tool call; parameters are never persisted.',
            )}
          >
            <InfoCircleOutlined style={{ color: '#8c8c8c', cursor: 'pointer' }} />
          </Tooltip>
        </Space>
      }
      extra={
        <Space wrap>
          {!isOrgAdmin && <OrganizationSelect value={organization} onChange={setOrganization} />}
          <RefreshButton
            loading={rules.isFetching || (isSuperAdmin && policy.isFetching)}
            onClick={() => {
              void rules.refetch();
              if (isSuperAdmin) void policy.refetch();
            }}
          />
          {canManage && (isSuperAdmin || organizationID > 0) && (
            <Button
              type="primary"
              onClick={() =>
                open({
                  organization_id: organizationID,
                  name: '',
                  pattern: '',
                  action: 'observe',
                  sensitivity: 'normal',
                  enabled: true,
                })
              }
            >
              {t('security.add_rule', 'Add rule')}
            </Button>
          )}
        </Space>
      }
    >
      {isSuperAdmin && (
        <Card size="small" title={t('security.mode_title')} style={{ marginBottom: 16 }}>
          <Space direction="vertical" style={{ width: '100%' }}>
            <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
              {t('security.mode_note')}
            </Typography.Paragraph>
            <Select
              aria-label={t('security.mode_title')}
              style={{ width: 260, maxWidth: '100%' }}
              value={policy.data?.mode}
              loading={policy.isLoading || savingMode}
              disabled={!policy.data || policy.isError || savingMode}
              onChange={saveMode}
              options={[
                { value: 'observe', label: t('security.mode_observe') },
                { value: 'block', label: t('security.mode_block') },
              ]}
            />
            {policy.isError && (
              <Alert type="error" showIcon message={t('security.mode_load_error')} />
            )}
          </Space>
        </Card>
      )}
      {isSuperAdmin && (
        <Card size="small" title={t('security.inspection_mode_title')} style={{ marginBottom: 16 }}>
          <Space direction="vertical" style={{ width: '100%' }}>
            <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
              {t('security.inspection_mode_note')}
            </Typography.Paragraph>
            <Select
              aria-label={t('security.inspection_mode_title')}
              style={{ width: 260, maxWidth: '100%' }}
              value={inspectionPolicy.data?.mode}
              loading={inspectionPolicy.isLoading || savingInspectionMode}
              disabled={!inspectionPolicy.data || inspectionPolicy.isError || savingInspectionMode}
              onChange={saveInspectionMode}
              options={[
                { value: 'observe', label: t('security.mode_observe') },
                { value: 'block', label: t('security.mode_block') },
                { value: 'disabled', label: t('security.mode_disabled') },
              ]}
            />
            {inspectionPolicy.isError && (
              <Alert type="error" showIcon message={t('security.mode_load_error')} />
            )}
          </Space>
        </Card>
      )}
      <PageHelp note="ux.rules_scope" />
      <Alert type="info" showIcon message={t('ux.rules_effect')} style={{ marginBottom: 16 }} />
      {(rules.isError || profile.isError) && (
        <Alert
          type="error"
          showIcon
          message={t('security.rules_load_error', 'Could not load rules or permissions.')}
        />
      )}
      <Table<SecurityRule>
        rowKey="id"
        loading={rules.isLoading || profile.isLoading}
        dataSource={rules.data || []}
        pagination={{ pageSize: 20 }}
        scroll={{ x: 680 }}
        columns={[
          {
            title: t('security.rule_name', 'Name'),
            dataIndex: 'name',
            width: 130,
            onCell: () => ({ style: { overflowWrap: 'anywhere' as const } }),
          },
          {
            title: t('security.rule_scope', 'Scope'),
            width: 120,
            render: (_, rule) =>
              rule.organization_id === 0 ? (
                <Tag>{t('security.global', 'Global baseline')}</Tag>
              ) : (
                <ResourceName kind="organizations" id={rule.organization_id} />
              ),
          },
          {
            title: t('ux.rule_action'),
            dataIndex: 'action',
            width: 110,
            render: value => (
              <Tag color={value === 'block' ? 'red' : 'blue'}>
                {t(actionKey(value, 'tool_call'), value)}
              </Tag>
            ),
          },
          {
            title: t('security.rule_sensitivity', 'Rule sensitivity'),
            dataIndex: 'sensitivity',
            width: 110,
            render: value => (
              <Tag color={value === 'high' ? 'red' : 'blue'}>
                {t(`security.sensitivity_${value}`, value)}
              </Tag>
            ),
          },
          {
            title: t('security.enabled', 'Enabled'),
            dataIndex: 'enabled',
            width: 75,
            render: value => (value ? t('security.yes', 'Yes') : t('security.no', 'No')),
          },
          {
            title: t('common.actions', 'Actions'),
            key: 'actions',
            width: 120,
            fixed: isWideScreen ? ('right' as const) : undefined,
            onHeaderCell: () => ({ style: { whiteSpace: 'nowrap' } }),
            onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
            render: (_, rule) => (
              <Space size={4} wrap={false} style={{ whiteSpace: 'nowrap' }}>
                <TableAction size="small" onClick={() => setDetail(rule)}>
                  {t('ux.details')}
                </TableAction>
                <TableAction
                  size="small"
                  disabled={!canManage || (rule.organization_id === 0 && !isSuperAdmin)}
                  onClick={() => open(rule)}
                >
                  {t('security.edit', 'Edit')}
                </TableAction>
              </Space>
            ),
          },
        ]}
      />
      <Drawer
        title={<span style={{ overflowWrap: 'anywhere' }}>{detail?.name}</span>}
        open={!!detail}
        onClose={() => setDetail(null)}
        width="min(620px, 100vw)"
        className="ux-details"
      >
        {detail && (
          <>
            <Alert type="info" message={t('ux.rules_scope')} />
            <Descriptions
              column={1}
              items={[
                { key: 'id', label: t('security.rule_id'), children: detail.id },
                { key: 'version', label: t('security.version'), children: detail.version },
                {
                  key: 'action',
                  label: t('ux.rule_action'),
                  children: t(detail.action === 'block' ? 'ux.tool_block' : 'ux.observe'),
                },
              ]}
            />
            <Typography.Title level={5}>{t('ux.pattern')}</Typography.Title>
            <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{detail.pattern}</pre>
          </>
        )}
      </Drawer>
      <Modal
        title={t('security.rules_title', 'Tool Security Rules')}
        open={editing !== null}
        onCancel={() => {
          if (!saving) cancel();
        }}
        onOk={save}
        okText={t('ux.save_rule')}
        maskClosable={false}
        confirmLoading={saving}
        forceRender
      >
        <Form form={form} layout="vertical" labelWrap disabled={saving}>
          <Form.Item
            name="name"
            label={t('security.rule_name', 'Name')}
            rules={[{ required: true }, { max: 128 }]}
          >
            <Input maxLength={128} />
          </Form.Item>
          <Form.Item name="action" label={t('ux.rule_action')} rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'observe', label: t('ux.observe') },
                { value: 'block', label: t('ux.tool_block') },
              ]}
            />
          </Form.Item>
          <AdvancedFields key={editing?.id || 'new'} defaultOpen={!editing?.id}>
            <Form.Item
              name="pattern"
              label={t('security.pattern', 'Regular expression')}
              rules={[{ required: true }, { max: 4096 }]}
            >
              <Input.TextArea rows={5} maxLength={4096} />
            </Form.Item>
          </AdvancedFields>
          <Form.Item
            name="sensitivity"
            label={t('security.rule_sensitivity', 'Rule sensitivity')}
            extra={t(
              'security.sensitivity_note',
              'High sensitivity matches trigger webhooks for both observe and block actions.',
            )}
            rules={[{ required: true }]}
          >
            <Select
              options={[
                { value: 'normal', label: t('security.sensitivity_normal', 'Normal') },
                { value: 'high', label: t('security.sensitivity_high', 'High') },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="enabled"
            label={t('security.enabled', 'Enabled')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
};
