interface ResourceNameClient {
  get(
    path: string,
    config: { silent: boolean; validateStatus: (status: number) => boolean },
  ): Promise<{
    status: number;
    data?: { data?: { name?: string } };
  }>;
}

export function createResourceNameLookup(client: ResourceNameClient) {
  let active = 0;
  const waiting: (() => void)[] = [];

  return async (path: string): Promise<string> => {
    if (active >= 4) await new Promise<void>(resolve => waiting.push(resolve));
    else active++;
    try {
      const response = await client.get(path, {
        // ResourceName renders lookup failures in the cell instead of a global configuration alert.
        silent: true,
        // Historical records may reference deleted or inaccessible resources.
        validateStatus: status => (status >= 200 && status < 300) || status === 404,
      });
      return response.status === 404 ? '' : response.data?.data?.name || '';
    } finally {
      const next = waiting.shift();
      if (next) next();
      else active--;
    }
  };
}
