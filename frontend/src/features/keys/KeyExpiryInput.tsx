import React from 'react';
import { DatePicker, type DatePickerProps } from 'antd';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';

export const KeyExpiryInput: React.FC<
  Omit<DatePickerProps, 'value' | 'onChange'> & {
    value?: string | null;
    onChange?: (value: string | null) => void;
  }
> = ({ value, onChange, style, ...props }) => {
  const date = value ? dayjs(value) : null;
  return (
    <DatePicker
      {...props}
      showTime
      allowClear
      format="YYYY-MM-DD HH:mm:ss"
      style={{ width: '100%', ...style }}
      value={date?.isValid() ? date : null}
      onChange={selected => onChange?.(selected ? selected.toISOString() : null)}
    />
  );
};
