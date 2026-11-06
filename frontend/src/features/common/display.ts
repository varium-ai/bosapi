export function compactKey(key: {
  key_prefix?: string;
  key_suffix?: string;
  masked_key?: string;
}): string {
  const prefix = Array.from(key.key_prefix || key.masked_key || 'sk-')
    .slice(0, 3)
    .join('');
  // Legacy keys may not have a recorded suffix; never use prefix bytes as a fake tail.
  const visibleTail = key.masked_key?.match(/(?:\*|•|\.){2,}([^*•.]+)$/)?.[1];
  const suffix = Array.from(key.key_suffix || visibleTail || '')
    .slice(-3)
    .join('');
  return `${prefix}...${suffix}`;
}

export function usageWithLimit(
  used: string,
  limit: unknown,
  format: (value: number) => string,
): string {
  return limit == null ? used : `${used} / ${format(Number(limit))}`;
}
