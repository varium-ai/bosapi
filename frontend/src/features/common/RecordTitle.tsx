import React from 'react';
import { OverflowText } from './OverflowText';

export function RecordTitle({
  name,
  description,
}: {
  name: React.ReactNode;
  description?: string;
}) {
  return (
    <div className="console-record">
      {typeof name === 'string' ? (
        <OverflowText as="div" className="console-record-title">
          {name}
        </OverflowText>
      ) : (
        <div className="console-record-title">{name}</div>
      )}
      {description && (
        <OverflowText as="div" className="console-record-description">
          {description}
        </OverflowText>
      )}
    </div>
  );
}
