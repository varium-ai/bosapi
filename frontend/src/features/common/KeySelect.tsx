import React, { useEffect, useState } from 'react';
import { Select, Spin } from 'antd';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import {
  keyNameOptionsQueryOptions,
  keyNameOptionsInfiniteQueryOptions,
} from '../../api/adminQueries';
import { useI18n } from '../../i18n';
import { useDebouncedValue } from './useDebouncedValue';
import { SelectLoadError } from './ConsoleUX';

export const KeySelect: React.FC<{
  value?: number;
  organizationId?: number;
  placeholder?: string;
  style?: React.CSSProperties;
  onChange: (value?: number) => void;
}> = ({ value, organizationId, placeholder, style, onChange }) => {
  const { t } = useI18n();
  const [input, setInput] = useState('');
  const search = useDebouncedValue(input);
  const searchPending = search !== input;
  useEffect(() => {
    setInput('');
  }, [organizationId]);
  const query = useInfiniteQuery({
    ...keyNameOptionsInfiniteQueryOptions(organizationId, search),
    enabled: !searchPending,
  });
  const selected = useQuery({
    ...keyNameOptionsQueryOptions(organizationId, String(value || '')),
    enabled: !!value,
  });
  const options = (query.data?.pages.flatMap(page => page.items) || []).map(
    (key: { id: number; name: string }) => ({
      value: key.id,
      label: key.name,
    }),
  );
  const resolvedPlaceholder = placeholder || t('usage.filter_key_name', 'Filter Key');
  return (
    <Select
      allowClear
      onPopupScroll={event => {
        const target = event.currentTarget;
        if (
          target.scrollHeight - target.scrollTop - target.clientHeight < 40 &&
          query.hasNextPage &&
          !query.isFetching
        )
          void query.fetchNextPage();
      }}
      showSearch
      filterOption={false}
      aria-label={resolvedPlaceholder}
      placeholder={resolvedPlaceholder}
      style={{ width: 150, maxWidth: '100%', ...style }}
      value={value}
      onChange={id => {
        setInput('');
        onChange(id);
      }}
      searchValue={input}
      onSearch={setInput}
      onOpenChange={open => {
        if (!open) setInput('');
      }}
      loading={query.isFetching || searchPending}
      notFoundContent={
        query.isFetching || searchPending ? (
          <Spin size="small" />
        ) : query.isError ? null : (
          t('ux.no_data')
        )
      }
      options={searchPending ? [] : options}
      popupRender={menu => (
        <>
          {!searchPending && query.isError && (
            <SelectLoadError
              retry={() => {
                void query.refetch();
              }}
            />
          )}
          {menu}
        </>
      )}
      labelRender={option =>
        options.find((item: { value: number }) => item.value === option.value)?.label ?? (
          <>
            {selected.data?.items.find(item => item.id === Number(option.value))?.name ||
              `Key #${option.value}`}
          </>
        )
      }
    />
  );
};
