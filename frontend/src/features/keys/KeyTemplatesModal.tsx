import React, { useRef, useState } from 'react';
import { Button, Form, Input, Modal, Space, Table, Typography, message } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { LoadError, useDiscardChanges } from '../common/ConsoleUX';
import { ResourceName } from '../common/ResourceName';
import { TableAction } from '../common/TableAction';
import { KeyConfigurationFields } from './KeyConfigurationFields';
import {
  emptyKeyTemplateSettings,
  getKeyTemplateSettings,
  type KeyTemplate,
  keyTemplatesQueryOptions,
} from './keyTemplates';

export const KeyTemplatesModal: React.FC<{
  open: boolean;
  onClose: () => void;
  currentUser: { id: number; organization_id: number; role: string };
}> = ({ open, onClose, currentUser }) => {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<KeyTemplate | null>(null);
  const saving = useRef(false);
  const cancel = useDiscardChanges(form, () => setEditorOpen(false));
  const {
    data: templates = [],
    isLoading,
    isError,
    refetch,
  } = useQuery({ ...keyTemplatesQueryOptions(currentUser?.id), enabled: open });
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['key-templates', currentUser.id] });
  const save = useMutation({
    mutationFn: async (values: any) => {
      const payload = {
        name: values.name,
        settings: getKeyTemplateSettings({ ...editing?.settings, ...values }),
      };
      return editing
        ? apiClient.put(`/admin/key-templates/${editing.id}`, payload)
        : apiClient.post('/admin/key-templates', payload);
    },
    onSuccess: () => {
      setEditorOpen(false);
      form.resetFields();
      void invalidate();
      message.success(t('keys.template_saved'));
    },
    onSettled: () => {
      saving.current = false;
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => apiClient.delete(`/admin/key-templates/${id}`),
    onSuccess: () => {
      void invalidate();
      message.success(t('keys.template_deleted'));
    },
  });
  const openEditor = (template: KeyTemplate | null) => {
    setEditing(template);
    form.resetFields();
    form.setFieldsValue({
      ...(template
        ? getKeyTemplateSettings(template.settings)
        : emptyKeyTemplateSettings(currentUser.organization_id)),
      name: template?.name,
    });
    form.setFields(Object.keys(form.getFieldsValue(true)).map(name => ({ name, touched: false })));
    setEditorOpen(true);
  };
  return (
    <>
      <Modal
        className="console-form-modal"
        title={t('keys.templates_title')}
        open={open}
        onCancel={onClose}
        footer={null}
        width={800}
      >
        <Typography.Paragraph type="secondary">{t('keys.templates_private')}</Typography.Paragraph>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => openEditor(null)}
          style={{ marginBottom: 16 }}
        >
          {t('keys.template_new')}
        </Button>
        {isError && <LoadError retry={refetch} />}
        <Table
          rowKey="id"
          dataSource={templates}
          loading={isLoading}
          pagination={{ pageSize: 10 }}
          columns={[
            { title: t('keys.template_name'), dataIndex: 'name' },
            {
              title: t('keys.field_org'),
              render: (_, item: KeyTemplate) => (
                <ResourceName kind="organizations" id={item.settings.organization_id} />
              ),
            },
            {
              title: t('common.actions'),
              width: 90,
              render: (_, item: KeyTemplate) => (
                <Space>
                  <TableAction
                    label={t('common.edit')}
                    icon={<EditOutlined />}
                    onClick={() => openEditor(item)}
                  />
                  <TableAction
                    label={t('common.delete')}
                    icon={<DeleteOutlined />}
                    danger
                    disabled={remove.isPending}
                    onClick={() =>
                      Modal.confirm({
                        title: t('keys.template_delete_confirm'),
                        content: item.name,
                        okText: t('common.delete'),
                        cancelText: t('common.cancel'),
                        okButtonProps: { danger: true },
                        onOk: () => remove.mutateAsync(item.id),
                      })
                    }
                  />
                </Space>
              ),
            },
          ]}
        />
      </Modal>
      <Modal
        className="console-form-modal"
        title={editing ? t('keys.template_edit') : t('keys.template_new')}
        open={editorOpen}
        width={640}
        maskClosable={false}
        confirmLoading={save.isPending}
        okText={editing ? t('common.save') : t('common.create')}
        cancelText={t('common.cancel')}
        onCancel={() => {
          if (!saving.current) cancel();
        }}
        onOk={() => form.submit()}
      >
        <Form
          name="key-template"
          form={form}
          scrollToFirstError={{ block: 'center', focus: true }}
          layout="vertical"
          disabled={save.isPending}
          onFinish={values => {
            if (saving.current) return;
            saving.current = true;
            save.mutate(values);
          }}
        >
          <Form.Item
            name="name"
            label={t('keys.template_name')}
            rules={[
              { required: true, whitespace: true, message: t('keys.template_name_required') },
            ]}
          >
            <Input maxLength={100} />
          </Form.Item>
          <KeyConfigurationFields
            key={editing?.id || 'new'}
            enabled={editorOpen}
            currentUser={currentUser}
            showRollingLimits={false}
            showIPBlocklist={false}
          />
        </Form>
      </Modal>
    </>
  );
};
