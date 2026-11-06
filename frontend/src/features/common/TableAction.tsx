import React, { forwardRef } from 'react';
import { Dropdown, Tooltip, type MenuProps } from 'antd';
import { MoreOutlined } from '@ant-design/icons';
import { ToolbarButton, type ToolbarButtonProps } from './ToolbarButton';
import { useI18n } from '../../i18n';

export const TableAction = forwardRef<
  React.ComponentRef<typeof ToolbarButton>,
  ToolbarButtonProps & { label?: string }
>(function TableAction({ label, className, size = 'small', ...props }, ref) {
  const button = (
    <ToolbarButton
      {...props}
      ref={ref}
      size={size}
      aria-label={props['aria-label'] ?? label}
      className={['console-table-action', className].filter(Boolean).join(' ')}
    />
  );
  return label ? <Tooltip title={label}>{button}</Tooltip> : button;
});
export function TableActionMenu({
  menu,
  disabled,
  loading,
}: {
  menu: MenuProps;
  disabled?: boolean;
  loading?: boolean;
}) {
  const { t } = useI18n();
  return (
    <Dropdown trigger={['click']} placement="bottomRight" menu={menu} disabled={disabled}>
      <TableAction
        label={t('common.more', 'More')}
        icon={<MoreOutlined />}
        disabled={disabled}
        loading={loading}
      />
    </Dropdown>
  );
}
