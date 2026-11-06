import { Tag } from 'antd';

export function HttpStatusTag({ value }: { value?: number | string | null }) {
  const status = Number(value);
  if (!Number.isInteger(status) || status < 100 || status > 599) return <span>-</span>;
  return (
    <Tag
      bordered={false}
      color={
        status >= 500
          ? 'error'
          : status >= 400
            ? 'warning'
            : status >= 200 && status < 300
              ? 'success'
              : 'default'
      }
    >
      {status}
    </Tag>
  );
}
