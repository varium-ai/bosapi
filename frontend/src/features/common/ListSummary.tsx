import { Tooltip } from 'antd';
import { useState } from 'react';
import { TableAction } from './TableAction';
import { OverflowText } from './OverflowText';

export function ListSummary({
  items,
  empty,
  onMore,
  limit = 2,
}: {
  items: string[];
  empty: string;
  onMore?: () => void;
  limit?: number;
}) {
  const [moreOpen, setMoreOpen] = useState(false);
  if (!items.length) return <span className="console-list-empty">{empty}</span>;
  const remaining = items.length - limit;
  const more = <span>+{remaining}</span>;
  return (
    <div className="console-list-summary">
      {items.slice(0, limit).map((item, index) => (
        <OverflowText className="console-chip" key={`${item}-${index}`}>
          {item}
        </OverflowText>
      ))}
      {remaining > 0 && (
        <Tooltip
          open={moreOpen}
          onOpenChange={setMoreOpen}
          trigger={onMore ? ['hover', 'focus'] : ['hover', 'focus', 'click']}
          title={
            <div className="console-list-tooltip">
              {items.map((item, index) => (
                <div key={`${item}-${index}`}>{item}</div>
              ))}
            </div>
          }
        >
          <TableAction
            className="console-list-more"
            aria-label={`${items.length}: ${items.join(', ')}`}
            aria-haspopup={onMore ? 'dialog' : undefined}
            onClick={onMore}
            onKeyDown={event => {
              if (event.key === 'Escape' && moreOpen) {
                setMoreOpen(false);
                event.stopPropagation();
              }
            }}
          >
            {more}
          </TableAction>
        </Tooltip>
      )}
    </div>
  );
}
