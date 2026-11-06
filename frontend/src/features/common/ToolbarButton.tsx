import React, { forwardRef } from 'react';
import { Button, type ButtonProps } from 'antd';

export type ToolbarButtonProps = Omit<ButtonProps, 'type' | 'variant' | 'color' | 'ghost'>;

export const ToolbarButton = forwardRef<React.ComponentRef<typeof Button>, ToolbarButtonProps>(
  function ToolbarButton({ className, ...props }, ref) {
    return (
      <Button
        {...props}
        ref={ref}
        type="text"
        className={['console-toolbar-button', className].filter(Boolean).join(' ')}
      />
    );
  },
);
