import { ArrowRightOutlined } from '@ant-design/icons';
import { Alert, Button } from 'antd';
import { gatewayConfigQueryOptions } from '../../config/gateway';
import { useQuery } from '@tanstack/react-query';
import { useI18n } from '../../i18n';
import './gateway-readiness-notice.css';

export function GatewayReadinessNotice({ canConfigure = false }: { canConfigure?: boolean }) {
  const query = useQuery(gatewayConfigQueryOptions());
  const gateway = query.data;
  const { t } = useI18n();
  if (query.isPlaceholderData && !query.isError) return null;
  if (query.isError)
    return (
      <Alert
        className="gateway-readiness-notice"
        type="warning"
        showIcon
        message={t('qa.address_load_error')}
        action={
          <Button size="small" loading={query.isFetching} onClick={() => void query.refetch()}>
            {t('common.retry')}
          </Button>
        }
      />
    );
  if (gateway?.baseUrl) return null;
  return (
    <Alert
      className="gateway-readiness-notice"
      type="warning"
      showIcon
      message={t('qa.address_check')}
      description={t('qa.address_help')}
      action={
        canConfigure && (
          <Button size="small" href="/admin/api-address" target="_blank" rel="noopener noreferrer">
            {t('qa.configure_address')}
            <ArrowRightOutlined />
          </Button>
        )
      }
    />
  );
}
