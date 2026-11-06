import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Empty,
  Form,
  Input,
  Modal,
  Pagination,
  Popconfirm,
  Skeleton,
  Space,
  Tabs,
  Tag,
} from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useOutletContext } from 'react-router-dom';
import type { Announcement, Publication } from './data';
import { announcementLoadErrorKey, useAnnouncements } from './data';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { AnnouncementMarkdown } from './AnnouncementMarkdown';
import './announcements.css';

export function AnnouncementsPage() {
  const { user } = useOutletContext<{
    user: { role: string; organization_id: number; id: number } | null;
  }>();
  const scope = `${user?.role}:${user?.organization_id}:${user?.id}`;
  const { t, formatDateTime } = useI18n();
  const audience = t(
    user?.role === 'org_admin' ? 'announcements.organization_audience' : 'announcements.audience',
  );
  const client = useQueryClient();
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Announcement | null>(null);
  const [tab, setTab] = useState('write');
  const [form] = Form.useForm<Publication>();
  const body = Form.useWatch('body', form) || '';
  const query = useAnnouncements(scope, page, false, true);
  const refresh = () => client.invalidateQueries({ queryKey: ['announcements'] });
  const save = useMutation({
    mutationFn: (values: Publication) =>
      editing
        ? apiClient.put(`/admin/announcements/${editing.id}`, values)
        : apiClient.post('/admin/announcements', values),
    onSuccess: () => {
      setOpen(false);
      if (!editing) setPage(1);
      form.resetFields();
      void refresh();
    },
  });
  const openEditor = (item: Announcement | null) => {
    save.reset();
    setEditing(item);
    form.resetFields();
    form.setFieldsValue(item ? { title: item.title, body: item.body } : { title: '', body: '' });
    setTab('write');
    setOpen(true);
  };
  const remove = useMutation({
    mutationFn: (id: number) => apiClient.delete(`/admin/announcements/${id}`),
    onSuccess: () => {
      if (page > 1 && query.data?.items.length === 1) setPage(page - 1);
      void refresh();
    },
  });
  return (
    <>
      <Card
        className="announcement-management"
        title={t('announcements.manage')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openEditor(null)}>
            {t('announcements.create')}
          </Button>
        }
      >
        <p className="announcement-help">{audience}</p>
        {query.isError && (
          <Alert
            type="warning"
            showIcon
            message={t(announcementLoadErrorKey(query.error))}
            action={
              <Button size="small" onClick={() => query.refetch()}>
                {t('common.retry')}
              </Button>
            }
          />
        )}
        {query.isLoading ? (
          <Skeleton active paragraph={{ rows: 5 }} />
        ) : (
          query.data && (
            <>
              {query.data.items.length ? (
                query.data.items.map(item => (
                  <article className="announcement-item" key={item.id}>
                    <div className="announcement-heading">
                      <h3>{item.title}</h3>
                      <Space>
                        <Button
                          size="small"
                          icon={<EditOutlined />}
                          onClick={() => openEditor(item)}
                          aria-label={`${t('common.edit')}: ${item.title}`}
                        >
                          {t('common.edit')}
                        </Button>
                        <Popconfirm
                          title={t('announcements.delete_confirm')}
                          description={t('announcements.delete_description')}
                          okText={t('common.delete')}
                          cancelText={t('common.cancel')}
                          okButtonProps={{ danger: true, loading: remove.isPending }}
                          onConfirm={() => remove.mutateAsync(item.id)}
                        >
                          <Button
                            type="text"
                            danger
                            size="small"
                            icon={<DeleteOutlined />}
                            aria-label={`${t('common.delete')}: ${item.title}`}
                          />
                        </Popconfirm>
                      </Space>
                    </div>
                    <Tag>
                      {t(
                        item.organization_id
                          ? 'announcements.scope_organization'
                          : 'announcements.scope_global',
                      )}
                    </Tag>
                    <time dateTime={item.created_at}>{formatDateTime(item.created_at)}</time>
                    <AnnouncementMarkdown body={item.body} />
                  </article>
                ))
              ) : !query.isError ? (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t('announcements.not_created')}
                />
              ) : null}
              {query.data.total > 5 && (
                <Pagination
                  size="small"
                  current={page}
                  pageSize={5}
                  total={query.data.total}
                  showSizeChanger={false}
                  onChange={setPage}
                />
              )}
            </>
          )
        )}
      </Card>
      <Modal
        forceRender
        title={t(editing ? 'announcements.edit' : 'announcements.create')}
        open={open}
        width={720}
        onCancel={() => {
          if (!save.isPending) setOpen(false);
        }}
        onOk={() => form.submit()}
        okText={t(editing ? 'common.save' : 'announcements.publish')}
        cancelText={t('common.cancel')}
        confirmLoading={save.isPending}
        cancelButtonProps={{ disabled: save.isPending }}
        maskClosable={!save.isPending}
      >
        <p className="announcement-help">{audience}</p>
        {save.isError && (
          <Alert
            type="error"
            showIcon
            message={t(editing ? 'announcements.save_failed' : 'announcements.publish_failed')}
          />
        )}
        <Form
          form={form}
          layout="vertical"
          onFinish={values => save.mutate(values)}
          onFinishFailed={() => setTab('write')}
          disabled={save.isPending}
        >
          <Form.Item
            name="title"
            label={t('announcements.title_label')}
            rules={[
              { required: true, whitespace: true, message: t('announcements.title_required') },
              {
                validator: async (_, value: string) => {
                  if (Array.from(value || '').length > 200)
                    throw new Error(t('announcements.title_limit'));
                },
              },
            ]}
          >
            <Input />
          </Form.Item>
          <Tabs
            activeKey={tab}
            onChange={setTab}
            items={[
              { key: 'write', label: t('announcements.write') },
              { key: 'preview', label: t('announcements.preview') },
            ]}
          />
          <div hidden={tab !== 'write'}>
            <Form.Item
              name="body"
              label={t('announcements.body_label')}
              extra={t('announcements.markdown_help')}
              rules={[
                { required: true, whitespace: true, message: t('announcements.body_required') },
                {
                  validator: async (_, value: string) => {
                    if (new TextEncoder().encode(value || '').length > 65536)
                      throw new Error(t('announcements.body_limit'));
                  },
                },
              ]}
            >
              <Input.TextArea rows={12} />
            </Form.Item>
          </div>
          {tab === 'preview' && (
            <div className="announcement-preview">
              <AnnouncementMarkdown body={body} />
            </div>
          )}
        </Form>
      </Modal>
    </>
  );
}
