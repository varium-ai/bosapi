import React from 'react';
import { Typography } from 'antd';
import { Link } from 'react-router-dom';

const { Text } = Typography;

export const RequestIDLink: React.FC<{ value?: string }> = ({ value }) => {
  if (!value) return <span>-</span>;
  const href = `/admin/request-logs?request_id=${encodeURIComponent(value)}`;
  return (
    <span className="console-request-id">
      <Link to={href} title={value}>
        <code>{value}</code>
      </Link>
      <Text copyable={{ text: value }} />
    </span>
  );
};
