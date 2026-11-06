import type { SupportedLanguage } from '../../i18n/types';

export interface OrganizationTokenRank {
  organization_id: number;
  organization_name: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface KeyTokenRank {
  api_key_id: number;
  api_key_name: string;
  organization_name?: string;
  organization_count?: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface ModelTokenRank {
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface TokenRankingItem {
  id: number | string;
  label: string;
  tooltipLabel?: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export function organizationRankingItems(
  items: OrganizationTokenRank[] | undefined,
  unknownUser: string,
): TokenRankingItem[] | undefined {
  return items?.map(item => ({
    ...item,
    id: item.organization_id,
    label:
      item.organization_name || (item.organization_id ? `#${item.organization_id}` : unknownUser),
  }));
}

export function keyRankingItems(
  items: KeyTokenRank[] | undefined,
  labels: { unnamedKey: string; unknownOrganization: string; multipleOrganizations?: string },
): TokenRankingItem[] | undefined {
  return items?.map(item => {
    const keyName = item.api_key_name.trim() || labels.unnamedKey;
    const organizationName =
      (item.organization_count || 0) > 1
        ? labels.multipleOrganizations || item.organization_name?.trim()
        : item.organization_name?.trim();
    const fullOrganization = organizationName || labels.unknownOrganization;
    const shortOrganization = organizationName
      ? Array.from(organizationName).slice(0, 5).join('')
      : fullOrganization;
    return {
      ...item,
      id: item.api_key_id,
      label: `${keyName} (${shortOrganization})`,
      tooltipLabel: `${keyName} (${fullOrganization})`,
    };
  });
}

export function modelRankingItems(
  items: ModelTokenRank[] | undefined,
  unknownModel: string,
): TokenRankingItem[] | undefined {
  return items?.map(item => ({
    ...item,
    id: item.model,
    label: item.model || unknownModel,
  }));
}

export function tokenPercentage(tokens: number, total: number): number {
  if (!Number.isFinite(tokens) || !Number.isFinite(total) || tokens <= 0 || total <= 0) return 0;
  return Math.min(100, (tokens / total) * 100);
}

export function formatUsageCount(count: number, language: SupportedLanguage = 'zh-CN'): string {
  const value = Number.isFinite(count) ? count : 0;
  if (language !== 'zh-CN' && language !== 'en-US') {
    return new Intl.NumberFormat(language, {
      notation: 'compact',
      maximumFractionDigits: 2,
    }).format(value);
  }
  const units: readonly (readonly [number, string])[] =
    language === 'zh-CN'
      ? [
          [100_000_000, '亿'],
          [1_000_000, '百万'],
          [10_000, '万'],
        ]
      : [
          [1_000_000_000, 'B'],
          [1_000_000, 'M'],
          [1_000, 'K'],
        ];
  const unit = units.find(([divisor]) => Math.abs(value) >= divisor);
  const formatted = new Intl.NumberFormat(language, {
    maximumFractionDigits: unit ? 2 : 0,
  }).format(unit ? value / unit[0] : value);
  return unit ? `${formatted} ${unit[1]}` : formatted;
}
