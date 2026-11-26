export class PartialKeySaveError extends Error {
  constructor(readonly originalError: unknown) {
    super('rolling limits were saved but the key update was not confirmed');
    this.name = 'PartialKeySaveError';
  }
}

export async function saveKeyPolicy<T>(
  saveLimits: () => Promise<unknown>,
  saveKey: () => Promise<T>,
): Promise<T> {
  await saveLimits();
  try {
    return await saveKey();
  } catch (error) {
    throw new PartialKeySaveError(error);
  }
}
