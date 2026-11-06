import type { ReactNode } from 'react';
import './toolbar.css';

export function ConsoleToolbar({
  children,
  actions,
  className,
}: {
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={['console-toolbar', className].filter(Boolean).join(' ')}>
      <div className="console-toolbar-filters">{children}</div>
      {actions && <div className="console-toolbar-actions">{actions}</div>}
    </div>
  );
}
