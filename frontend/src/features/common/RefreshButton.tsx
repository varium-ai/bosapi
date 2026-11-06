import React, { forwardRef } from 'react';
import { ReloadOutlined } from '@ant-design/icons';
import { useI18n } from '../../i18n';
import { ToolbarButton, type ToolbarButtonProps } from './ToolbarButton';

type RefreshButtonProps = Omit<ToolbarButtonProps, 'icon' | 'loading'> & { loading?: boolean };

export const RefreshButton = forwardRef<
  React.ComponentRef<typeof ToolbarButton>,
  RefreshButtonProps
>(function RefreshButton({ children, loading = false, ...props }, ref) {
  const { t } = useI18n();
  return (
    <ToolbarButton
      {...props}
      ref={ref}
      aria-busy={loading}
      icon={<ReloadOutlined aria-hidden="true" />}
      loading={loading ? { icon: <ReloadOutlined spin aria-hidden="true" /> } : false}
    >
      {children ?? t('common.refresh', 'Refresh')}
    </ToolbarButton>
  );
});
