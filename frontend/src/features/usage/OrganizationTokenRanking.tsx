import { Card, Empty, Tabs, Tooltip } from 'antd';
import { useI18n } from '../../i18n';
import {
  formatUsageCount,
  keyRankingItems,
  modelRankingItems,
  organizationRankingItems,
  tokenPercentage,
  type TokenRankingItem,
  type KeyTokenRank,
  type ModelTokenRank,
  type OrganizationTokenRank,
} from './tokenRanking';
import './userTokenRanking.css';

export function OrganizationTokenRanking({
  activeTab,
  onTabChange,
  items,
  keys,
  models,
  totalTokens,
  from,
  to,
}: {
  activeTab: string;
  onTabChange: (tab: string) => void;
  items?: OrganizationTokenRank[];
  keys?: KeyTokenRank[];
  models?: ModelTokenRank[];
  totalTokens: number;
  from: string;
  to: string;
}) {
  const { t } = useI18n();
  return (
    <Card size="small" title={t('usage.ranking_dashboard_title')} className="user-token-ranking">
      <p className="user-token-ranking-caption">
        <span>{from === to ? from : `${from} — ${to}`} · UTC+8</span>
        <span>{t('usage.ranking_note')}</span>
      </p>
      <Tabs
        activeKey={activeTab}
        onChange={onTabChange}
        items={[
          {
            key: 'organizations',
            label: t('usage.ranking_users_tab'),
            children: (
              <RankingBars
                items={organizationRankingItems(items, t('usage.unknown_user'))}
                totalTokens={totalTokens}
                title={t('usage.ranking_title')}
              />
            ),
          },
          {
            key: 'keys',
            label: t('usage.ranking_keys_tab'),
            children: (
              <RankingBars
                items={keyRankingItems(keys, {
                  unnamedKey: t('usage.unnamed_key'),
                  unknownOrganization: t('usage.unknown_user'),
                  multipleOrganizations: t('usage.multiple_organizations'),
                })}
                totalTokens={totalTokens}
                title={t('usage.ranking_keys_title')}
              />
            ),
          },
          {
            key: 'models',
            label: t('usage.ranking_models_tab'),
            children: (
              <RankingBars
                items={modelRankingItems(models, t('usage.unknown_model'))}
                totalTokens={totalTokens}
                title={t('usage.ranking_models_title')}
              />
            ),
          },
        ]}
      />
    </Card>
  );
}

function RankingBars({
  items,
  totalTokens,
  title,
}: {
  items?: TokenRankingItem[];
  totalTokens: number;
  title: string;
}) {
  const { t, formatNumber, language } = useI18n();
  const maximum = Math.max(0, ...(items || []).map(item => item.total_tokens));
  return (
    <>
      {!items ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('usage.ranking_unavailable')} />
      ) : !items.length ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('usage.ranking_empty')} />
      ) : (
        <ol className="user-token-ranking-list" aria-label={title}>
          {items.map((item, index) => {
            const share = tokenPercentage(item.total_tokens, totalTokens);
            return (
              <li key={item.id} className="user-token-ranking-row">
                <span className="user-token-ranking-position">{index + 1}</span>
                <Tooltip title={item.tooltipLabel || item.label}>
                  <span className="user-token-ranking-person">{item.label}</span>
                </Tooltip>
                <div className="user-token-ranking-track" aria-hidden="true">
                  <div
                    className="user-token-ranking-fill"
                    style={{ width: `${tokenPercentage(item.total_tokens, maximum)}%` }}
                  />
                </div>
                <Tooltip
                  trigger={['hover', 'focus']}
                  title={
                    <>
                      {t('usage.input_tokens')}: {formatNumber(item.input_tokens)}
                      <br />
                      {t('usage.output_tokens')}: {formatNumber(item.output_tokens)}
                      <br />
                      {t('usage.total_tokens')}: {formatNumber(item.total_tokens)}
                    </>
                  }
                >
                  <span className="user-token-ranking-value" tabIndex={0}>
                    {formatUsageCount(item.total_tokens, language)}
                  </span>
                </Tooltip>
                <span className="user-token-ranking-share" title={t('usage.ranking_share')}>
                  {formatNumber(share, 1)}%
                </span>
              </li>
            );
          })}
        </ol>
      )}
    </>
  );
}
