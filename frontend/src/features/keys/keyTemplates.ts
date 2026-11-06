import { queryOptions } from '@tanstack/react-query';
import { apiClient } from '../../api/client';

import { type KeyTemplate } from './templateSettings';
export { emptyKeyTemplateSettings, getKeyTemplateSettings } from './templateSettings';
export type { KeyTemplate, KeyTemplateSettings } from './templateSettings';

export function keyTemplatesQueryOptions(userID: number) {
  return queryOptions({
    queryKey: ['key-templates', userID],
    queryFn: async () => {
      const items: KeyTemplate[] = [];
      for (let page = 1; ; page++) {
        const response = await apiClient.get('/admin/key-templates', {
          params: { page, page_size: 100 },
        });
        const data = response.data.data;
        items.push(...data.items);
        if (data.items.length < 100 || items.length >= data.total) return items;
      }
    },
  });
}
