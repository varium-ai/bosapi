export interface ChannelPricing {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
  confirmed?: boolean;
  models?: Record<
    string,
    { input?: number; output?: number; cache_read?: number; cache_write?: number }
  >;
}

export function defaultPriceConfirmed(pricing?: ChannelPricing): boolean {
  return (
    !!pricing &&
    (pricing.confirmed === true ||
      (Number.isFinite(pricing.input) && Number(pricing.input) > 0) ||
      (Number.isFinite(pricing.output) && Number(pricing.output) > 0))
  );
}

export function modelPriceSource(
  pricing: ChannelPricing | undefined,
  model: {
    model_name: string;
    price_input_per_1m?: number;
    price_output_per_1m?: number;
    price_cache_read_per_1m?: number;
    price_cache_write_per_1m?: number;
  },
): 'model' | 'default' | 'recorded' | 'unknown' {
  const override = pricing?.models?.[model.model_name];
  if (override && Number.isFinite(override.input) && Number.isFinite(override.output))
    return 'model';
  if (
    defaultPriceConfirmed(pricing) &&
    model.price_input_per_1m === (pricing?.input ?? 0) &&
    model.price_output_per_1m === (pricing?.output ?? 0)
  )
    return 'default';
  if (Number(model.price_input_per_1m) > 0 || Number(model.price_output_per_1m) > 0)
    return 'recorded';
  return 'unknown';
}

export function saveDefaultPricing(
  existing: ChannelPricing | undefined,
  input?: number | null,
  output?: number | null,
  cacheRead?: number | null,
  cacheWrite?: number | null,
): ChannelPricing {
  if (input == null && output == null && cacheRead == null && cacheWrite == null)
    return { ...existing };
  // Paired values are validated by the form before submission.
  return {
    ...existing,
    input: Number(input ?? 0),
    output: Number(output ?? 0),
    cache_read: Number(cacheRead ?? existing?.cache_read ?? 0),
    cache_write: Number(cacheWrite ?? existing?.cache_write ?? 0),
    confirmed: true,
  };
}
