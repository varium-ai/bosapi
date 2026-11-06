import type { QueryClient } from '@tanstack/react-query';
import { apiClient } from '../../api/client';
import {
  collectSetupPages,
  hasSetupModel,
  type SetupOrganization,
  type SetupProvider,
  type SetupEvidence,
} from './firstKey';

export { parseSetupEvidence, type SetupEvidence } from './firstKey';

export const FIRST_KEY_QUERY = 'first-key-onboarding';

export async function loadSetupPages<T>(
  endpoint: string,
  signal: AbortSignal,
  params: Record<string, unknown> = {},
) {
  return collectSetupPages<T>(async page => {
    const response = await apiClient.get(endpoint, {
      params: { ...params, page, page_size: 100 },
      signal,
      skipGlobalErrorMessage: true,
    });
    return response.data?.data;
  });
}

export async function loadSetupReadiness(
  organization: SetupOrganization | undefined,
  superAdmin: boolean,
  signal: AbortSignal,
  _legacyScope = false,
  evidence?: SetupEvidence,
) {
  if (evidence?.provider_id)
    return {
      channelReady: true,
      organizationReady: true,
      providerID: evidence.provider_id,
      verified: evidence.channel_verified,
      updatedAt: evidence.provider_updated_at || undefined,
      canTest: evidence.can_test,
    };
  const effective = organization
    ? await loadSetupPages<SetupProvider>('/admin/providers', signal, {
        organization_id: organization.id,
        status: 'active',
      })
    : [];
  const visible = effective;
  const modelCache = new Map<number, { model_name: string; status?: string }[]>();
  const ready = async (provider: SetupProvider, org?: SetupOrganization) => {
    if (provider.schedulable === false) return false;
    let models = modelCache.get(provider.id);
    if (!models) {
      models = await loadSetupPages<{ model_name: string; status?: string }>(
        `/admin/providers/${provider.id}/models`,
        signal,
        { status: 'active' },
      );
      modelCache.set(provider.id, models);
    }
    return hasSetupModel(provider, models, org);
  };
  const verifiedFirst = (a: SetupProvider, b: SetupProvider) =>
    Number(!!b.verified_at && Date.parse(b.verified_at) >= Date.parse(b.updated_at || '')) -
    Number(!!a.verified_at && Date.parse(a.verified_at) >= Date.parse(a.updated_at || ''));
  for (const provider of evidence ? [] : visible.sort(verifiedFirst)) {
    if (await ready(provider, organization))
      return {
        channelReady: true,
        organizationReady: true,
        providerID: provider.id,
        verified:
          !!provider.verified_at &&
          !!provider.updated_at &&
          Date.parse(provider.verified_at) >= Date.parse(provider.updated_at),
        updatedAt: provider.updated_at,
        canTest: superAdmin,
      };
  }
  const candidates = superAdmin
    ? await loadSetupPages<SetupProvider>('/admin/providers', signal, { status: 'active' })
    : [];
  for (const provider of candidates) {
    if (await ready(provider))
      return {
        channelReady: true,
        organizationReady: false,
        providerID: provider.id,
        verified:
          !!provider.verified_at &&
          !!provider.updated_at &&
          Date.parse(provider.verified_at) >= Date.parse(provider.updated_at),
        updatedAt: provider.updated_at,
        canTest: superAdmin,
      };
  }
  const editable = superAdmin ? [...visible, ...candidates][0] : undefined;
  return {
    channelReady: false,
    organizationReady: false,
    providerID: editable?.id,
    verified: false,
    updatedAt: editable?.updated_at,
    canTest: superAdmin,
  };
}

export function invalidateFirstKeySetup(client: QueryClient) {
  void client.invalidateQueries({ queryKey: [FIRST_KEY_QUERY] });
  void client.invalidateQueries({ queryKey: ['key-channel-options'] });
  void client.invalidateQueries({ queryKey: ['key-organization-readiness'] });
  void client.invalidateQueries({
    predicate: query => query.queryKey[0] === 'overview' && query.queryKey[2] === 'resource',
  });
}
