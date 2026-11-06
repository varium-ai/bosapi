import React from 'react';
import { Modal, Table, Tag, Typography } from 'antd';
import { useI18n } from '../../i18n';

const { Text } = Typography;

export interface AdminLoginRecord {
  id: number;
  client_ip?: string;
  created_at: string;
  success: boolean;
  reason?: string;
}

interface AdminLoginRecordsModalProps {
  open: boolean;
  userEmail?: string;
  records: AdminLoginRecord[];
  loading: boolean;
  page: number;
  total: number;
  onClose: () => void;
  onPageChange: (page: number) => void;
}

export const AdminLoginRecordsModal: React.FC<AdminLoginRecordsModalProps> = ({
  open,
  userEmail,
  records,
  loading,
  page,
  total,
  onClose,
  onPageChange,
}) => {
  const { t, formatDateTime } = useI18n();
  const title = userEmail
    ? `${t('adminusers.login_records_title', 'Login Records')} - ${userEmail}`
    : t('adminusers.login_records_title', 'Login Records');

  return (
    <Modal title={title} open={open} onCancel={onClose} footer={null} width={680}>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={records}
        scroll={{ x: 580 }}
        pagination={{ current: page, pageSize: 20, total, onChange: onPageChange }}
        columns={[
          {
            title: t('adminusers.login_ip', 'IP Address'),
            dataIndex: 'client_ip',
            width: 140,
            render: (value: string) => <Text code>{value || '-'}</Text>,
          },
          {
            title: t('adminusers.login_time', 'Login Time'),
            dataIndex: 'created_at',
            width: 180,
            render: (value: string) => formatDateTime(value),
          },
          {
            title: t('adminusers.login_result', 'Result'),
            dataIndex: 'success',
            width: 100,
            align: 'center' as const,
            render: (value: boolean) => (
              <Tag color={value ? 'success' : 'error'}>
                {value
                  ? t('adminusers.login_success', 'Success')
                  : t('adminusers.login_failed', 'Failed')}
              </Tag>
            ),
          },
          {
            title: t('adminusers.login_reason', 'Failure Reason'),
            dataIndex: 'reason',
            width: 160,
            render: (value: string) => value || '-',
          },
        ]}
      />
    </Modal>
  );
};

export default AdminLoginRecordsModal;
