import React, { useEffect, useState } from 'react';
import { Button, Tooltip } from 'antd';
import { useI18n } from '../i18n';

interface SidebarToggleProps {
  expanded: boolean;
  onClick: () => void;
}

export const SidebarToggle = React.forwardRef<HTMLButtonElement, SidebarToggleProps>(
  ({ expanded, onClick }, ref) => {
    const { t } = useI18n();
    const [tooltipOpen, setTooltipOpen] = useState(false);
    useEffect(() => setTooltipOpen(false), [expanded]);
    const label = t(expanded ? 'ux.sidebar_collapse' : 'ux.sidebar_expand');
    return (
      <Tooltip
        open={tooltipOpen}
        onOpenChange={setTooltipOpen}
        title={
          <span className="console-sidebar-tooltip">
            {label}
            <kbd>{/Mac|iPhone|iPad/.test(navigator.platform) ? '\u2318 B' : 'Ctrl+B'}</kbd>
          </span>
        }
        placement="bottom"
      >
        <Button
          ref={ref}
          type="text"
          className="console-sidebar-toggle"
          onClick={() => {
            setTooltipOpen(false);
            onClick();
          }}
          aria-label={label}
          aria-expanded={expanded}
          aria-controls="console-navigation"
          aria-keyshortcuts="Meta+B Control+B"
          icon={
            <svg
              width="20"
              height="20"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <rect x="3.5" y="4" width="17" height="16" rx="3" />
              <path d="M9 4v16" />
            </svg>
          }
        />
      </Tooltip>
    );
  },
);
SidebarToggle.displayName = 'SidebarToggle';
