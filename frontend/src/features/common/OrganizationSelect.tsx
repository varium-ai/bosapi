import React, { useState } from 'react';
import { Select, Spin } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import { useI18n } from '../../i18n';
import { useDebouncedValue } from './useDebouncedValue';
import { ResourceName } from './ResourceName';
import { SelectLoadError } from './ConsoleUX';

export const OrganizationSelect: React.FC<{ value: number; onChange: (id: number) => void }> = ({
  value,
  onChange,
}) => {
  const { t } = useI18n();
  const [input, setInput] = useState('');
  const search = useDebouncedValue(input);
  const searchPending = search !== input;
  const query = useQuery({
    queryKey: ['ux-organizations', search],
    enabled: !searchPending,
    queryFn: async () =>
      (await apiClient.get('/admin/organizations', { params: { search, page: 1, page_size: 100 } }))
        .data.data,
    staleTime: 30000,
  });
  const options = [
    { value: 0, label: t('ux.baseline') },
    ...(query.data?.items || []).map((item: { id: number; name: string }) => ({
      value: item.id,
      label: item.name,
    })),
  ];
  return (
    <Select
      aria-label={t('ux.organization')}
      style={{ width: 220, maxWidth: '100%', minWidth: 0 }}
      value={value}
      onChange={id => {
        setInput('');
        onChange(id);
      }}
      showSearch
      searchValue={input}
      onSearch={setInput}
      onOpenChange={open => {
        if (!open) setInput('');
      }}
      filterOption={false}
      loading={query.isFetching || searchPending}
      notFoundContent={
        query.isFetching || searchPending ? (
          <Spin size="small" />
        ) : query.isError ? null : (
          t('ux.no_data')
        )
      }
      options={searchPending ? [] : options}
      labelRender={option =>
        options.find(item => item.value === option.value)?.label ?? (
          <ResourceName kind="organizations" id={Number(option.value)} link={false} />
        )
      }
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
    />
  );
};
