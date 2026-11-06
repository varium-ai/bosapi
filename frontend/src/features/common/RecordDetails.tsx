import { Descriptions, type DescriptionsProps } from 'antd';
import './record-details.css';

export function RecordDetails({
  className,
  size = 'small',
  ...props
}: Omit<DescriptionsProps, 'column' | 'bordered'>) {
  return (
    <Descriptions
      {...props}
      column={1}
      bordered
      size={size}
      className={['console-record-details', className].filter(Boolean).join(' ')}
    />
  );
}
