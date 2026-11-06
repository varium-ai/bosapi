import React from 'react';
import { Alert, Button, Modal, Space, Table, Typography, Upload } from 'antd';
import { DownloadOutlined, UploadOutlined } from '@ant-design/icons';
import { useI18n } from '../../i18n';
import { API_KEY_IMPORT_EXAMPLE } from './importExample';

export const KeyImportGuide: React.FC<{
  open: boolean;
  parsing: boolean;
  onClose: () => void;
  onFile: (file: File) => Promise<boolean>;
}> = ({ open, parsing, onClose, onFile }) => {
  const { t } = useI18n();
  return (
    <Modal
      title={t('keys.import_guide_title')}
      open={open}
      width={640}
      maskClosable={!parsing}
      closable={!parsing}
      onCancel={() => {
        if (!parsing) onClose();
      }}
      footer={
        <Space>
          <Button disabled={parsing} onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Upload
            accept=".csv,.xls,.xlsx"
            showUploadList={false}
            beforeUpload={onFile}
            disabled={parsing}
          >
            <Button type="primary" icon={<UploadOutlined />} loading={parsing}>
              {t('keys.import_choose_file')}
            </Button>
          </Upload>
        </Space>
      }
    >
      <Typography.Paragraph>{t('keys.import_supported_files')}</Typography.Paragraph>
      <Typography.Paragraph>{t('keys.import_file_layout')}</Typography.Paragraph>
      <Table
        size="small"
        pagination={false}
        rowKey="name"
        dataSource={[
          { name: 'Production', api_key: 'replace-with-your-api-key-1' },
          { name: 'Staging', api_key: 'replace-with-your-api-key-2' },
        ]}
        columns={[
          { title: 'name', dataIndex: 'name' },
          { title: 'api_key', dataIndex: 'api_key' },
        ]}
      />
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {t('keys.import_columns_help')}
      </Typography.Paragraph>
      <Button
        type="link"
        icon={<DownloadOutlined />}
        href={`data:text/csv;charset=utf-8,${encodeURIComponent('\uFEFF' + API_KEY_IMPORT_EXAMPLE)}`}
        download="api-key-import-example.csv"
        style={{ paddingLeft: 0 }}
      >
        {t('keys.import_download_example')}
      </Button>
      <Alert
        type="info"
        showIcon
        message={t('keys.import_next_step')}
        description={t('keys.import_example_notice')}
        style={{ marginTop: 12 }}
      />
    </Modal>
  );
};
