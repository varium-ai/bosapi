import { Button, Result } from 'antd';
import { useI18n } from '../i18n';

export default function ForbiddenPage() {
  const { t } = useI18n();
  return (
    <div style={{ padding: 48, display: 'flex', justifyContent: 'center' }}>
      <Result
        status="403"
        title={t('routes.forbidden_title', '403 Forbidden')}
        subTitle={t('routes.forbidden_sub', 'You do not have permission to access this page.')}
        extra={
          <Button type="primary" onClick={() => (window.location.href = '/')}>
            {t('routes.back_home', 'Back to Overview')}
          </Button>
        }
      />
    </div>
  );
}
