import { forwardRef, useRef } from 'react';
import { Input, type InputRef } from 'antd';
import type { SearchProps } from 'antd/es/input';
import { LoadingOutlined, SearchOutlined } from '@ant-design/icons';
import { useI18n } from '../../i18n';

export const SearchInput = forwardRef<InputRef, Omit<SearchProps, 'enterButton'>>(
  function SearchInput(
    {
      onSearch,
      onChange,
      onPressEnter,
      onCompositionStart,
      onCompositionEnd,
      loading,
      prefix,
      className,
      ...props
    },
    ref,
  ) {
    const { t } = useI18n();
    const composing = useRef(false);
    return (
      <Input
        aria-label={t('common.search', 'Search')}
        enterKeyHint="search"
        allowClear
        {...props}
        ref={ref}
        className={['console-search-input', className].filter(Boolean).join(' ')}
        prefix={loading ? <LoadingOutlined spin /> : (prefix ?? <SearchOutlined />)}
        onChange={event => {
          if (event.type === 'click' && !event.target.value)
            onSearch?.('', event, { source: 'clear' });
          onChange?.(event);
        }}
        onCompositionStart={event => {
          composing.current = true;
          onCompositionStart?.(event);
        }}
        onCompositionEnd={event => {
          composing.current = false;
          onCompositionEnd?.(event);
        }}
        onPressEnter={event => {
          if (composing.current || event.nativeEvent.isComposing || loading) return;
          onPressEnter?.(event);
          if (!event.defaultPrevented)
            onSearch?.(event.currentTarget.value, event, { source: 'input' });
        }}
      />
    );
  },
);
