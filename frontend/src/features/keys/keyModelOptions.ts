export interface KeyChannelOption {
  id: number;
  name: string;
  schedulable?: boolean;
  status?: string;
}

export interface KeyChannelModel {
  model_name: string;
  status?: string;
}

export function keyModelChannels(channels: KeyChannelOption[], selected: number[] = []) {
  const selectedIDs = new Set(selected);
  return channels
    .filter(
      channel =>
        channel.schedulable !== false &&
        (!channel.status || channel.status === 'active') &&
        (!selectedIDs.size || selectedIDs.has(channel.id)),
    )
    .sort((a, b) => a.id - b.id);
}

// The provider endpoint is paginated, including when a channel has more than 100 models.
export async function loadKeyChannelModels(
  fetchPage: (page: number) => Promise<{ items: KeyChannelModel[]; total?: number }>,
) {
  const models: KeyChannelModel[] = [];
  for (let page = 1; ; page++) {
    const data = await fetchPage(page);
    models.push(...data.items);
    if (
      !data.items.length ||
      (data.total !== undefined ? models.length >= data.total : data.items.length < 100)
    )
      return models;
  }
}

export function buildKeyModelOptions(
  catalogs: { channel: KeyChannelOption; models: KeyChannelModel[] }[],
) {
  const sources = new Map<string, Set<string>>();
  for (const { channel, models } of catalogs) {
    for (const model of models) {
      if (!model.model_name || (model.status && model.status !== 'active')) continue;
      const names = sources.get(model.model_name) || new Set<string>();
      names.add(channel.name);
      sources.set(model.model_name, names);
    }
  }
  return Array.from(sources, ([value, names]) => ({
    value,
    label: `${value} — ${Array.from(names).join(', ')}`,
  })).sort((a, b) => a.value.localeCompare(b.value));
}
