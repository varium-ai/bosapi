import { GatewayReadinessNotice } from './GatewayReadinessNotice';
import { lazy, Suspense, useState } from 'react';
import { Alert, Button, Select, Skeleton } from 'antd';
import { ArrowRightOutlined, CheckOutlined, ReloadOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import { useI18n } from '../../i18n';
import { apiClient } from '../../api/client';
import {
  firstKeyPath,
  setupProgress,
  parseSetupPreferences,
  positiveID,
  selectSetupOrganization,
  setupOrganizations,
  type SetupOrganization,
  type SetupUser,
} from './firstKey';
import {
  FIRST_KEY_QUERY,
  loadSetupPages,
  loadSetupReadiness,
  type SetupEvidence,
  parseSetupEvidence,
} from './setupQueries';
const VerifyCall = lazy(() =>
  import('./VerifyCall').then(module => ({ default: module.VerifyCall })),
);
import './first-key.css';

export function FirstKeyGuide({ user }: { user: SetupUser | null }) {
  const { pathname } = useLocation();
  const [params] = useSearchParams();
  if (!user || !['super_admin', 'org_admin'].includes(user.role)) return null;
  const guided = params.get('onboarding') === 'first-key';
  if (
    pathname !== '/' &&
    !(guided && ['/admin/providers', '/admin/organizations', '/admin/keys'].includes(pathname))
  )
    return null;
  return (
    <FirstKeyProgress
      key={`${user.role}:${user.id}:${user.organization_id}`}
      user={user}
      guided={guided}
    />
  );
}
function FirstKeyProgress({ user, guided }: { user: SetupUser; guided: boolean }) {
  const { t, language } = useI18n();
  const zh = language === 'zh-CN';
  const client = useQueryClient();
  const [params, setParams] = useSearchParams();
  const { pathname } = useLocation();
  const superAdmin = user.role === 'super_admin';
  const scope = `${user.role}:${user.id}:${user.organization_id}`;
  const organizations = useQuery({
    queryKey: [FIRST_KEY_QUERY, scope, 'organizations'],
    queryFn: async ({ signal }) => {
      const items = await loadSetupPages<SetupOrganization>('/admin/organizations', signal);
      if (
        items.some(
          item =>
            !Number.isSafeInteger(item.id) ||
            item.id <= 0 ||
            typeof item.has_api_keys !== 'boolean' ||
            !['active', 'disabled'].includes(item.status),
        )
      )
        throw new Error('Incomplete organization status');
      return setupOrganizations(items, user);
    },
    staleTime: 15_000,
    retry: false,
  });
  const items = organizations.data || [];
  const requested = superAdmin ? positiveID(params.get('setup_org')) : user.organization_id;
  const organization = selectSetupOrganization(items, requested) || items[0];
  const status = useQuery({
    queryKey: [FIRST_KEY_QUERY, scope, 'evidence', organization?.id],
    queryFn: async ({ signal }) =>
      parseSetupEvidence(
        (
          await apiClient.get<{ data: SetupEvidence }>(
            `/admin/organizations/${organization!.id}/setup-status`,
            {
              signal,
              skipGlobalErrorMessage: true,
            },
          )
        ).data.data,
      ),
    enabled: !!organization,
    staleTime: 15_000,
    retry: false,
  });
  const readiness = useQuery({
    queryKey: [FIRST_KEY_QUERY, scope, 'readiness', organization, status.data],
    queryFn: ({ signal }) =>
      loadSetupReadiness(organization, superAdmin, signal, false, status.data),
    enabled:
      organizations.isSuccess &&
      (!organization || status.isSuccess) &&
      (!!organization || superAdmin),
    staleTime: 15_000,
    retry: false,
  });
  const [paused, setPaused] = useState<Record<string, boolean>>(() => {
    try {
      return parseSetupPreferences(localStorage.getItem(`setup-preference:${scope}`));
    } catch {
      return {};
    }
  });
  const [verify, setVerify] = useState(false);
  const preferenceID = String(organization?.id || 'instance');
  const setPause = (value: boolean) => {
    const next = { ...paused, [preferenceID]: value };
    setPaused(next);
    try {
      localStorage.setItem(`setup-preference:${scope}`, JSON.stringify(next));
    } catch {
      /* Preferences are optional; resource progress always comes from the server. */
    }
  };
  const refresh = () => void client.invalidateQueries({ queryKey: [FIRST_KEY_QUERY, scope] });
  const resume = () => {
    setPause(false);
    const next = new URLSearchParams(params);
    next.set('onboarding', 'first-key');
    setParams(next, { replace: true });
    refresh();
  };
  const failed = organizations.isError || readiness.isError || status.isError;
  const loading =
    organizations.isPending ||
    (readiness.isEnabled && readiness.isPending) ||
    (status.isEnabled && status.isPending);
  const ready = readiness.data;
  const { order, completed, current, count, successful } = setupProgress({
    organizationExists: !!organization,
    channelConfigured: !!ready?.channelReady,
    channelVerified: !!ready?.verified,
    organizationReady: !!ready?.organizationReady,
    hasUsableKey: !!status.data?.has_usable_key,
    successfulCallAt: status.data?.successful_call_at,
    providerUpdatedAt: ready?.updatedAt,
  });
  if ((paused[preferenceID] || successful) && !guided)
    return (
      <div className="first-key-resume">
        <Button type="link" onClick={resume}>
          {zh ? '重新进入接入引导' : 'Open setup guide'}
        </Button>
      </div>
    );
  if (failed)
    return (
      <Alert
        className="first-key-guide"
        type="warning"
        showIcon
        message={t('onboarding.load_failed')}
        action={
          <Button size="small" onClick={refresh}>
            {t('common.retry')}
          </Button>
        }
      />
    );
  if (loading)
    return (
      <div className="first-key-guide" aria-label={t('onboarding.loading')} aria-busy="true">
        <Skeleton active paragraph={{ rows: 1 }} />
      </div>
    );
  if (!organization && !superAdmin)
    return (
      <Alert
        type="warning"
        showIcon
        message={
          zh
            ? '本组织不可用，请联系本实例超级管理员恢复组织后继续。'
            : 'Your organization is unavailable. Ask your instance super administrator to restore it.'
        }
      />
    );
  let target = firstKeyPath('/admin/keys', organization?.id, 'create-key');
  let action = t('onboarding.create_key');
  const blocked = current === 'channel' && !superAdmin;
  if (current === 'organization') {
    target = firstKeyPath(
      '/admin/organizations',
      organization?.id,
      organization ? 'edit-organization' : 'create-organization',
    );
    action = t(organization ? 'onboarding.assign_channel' : 'onboarding.create_organization');
  }
  if (current === 'channel') {
    target = firstKeyPath(
      '/admin/providers',
      organization?.id,
      ready?.channelReady && !ready.verified
        ? 'test-provider'
        : ready?.providerID
          ? 'edit-provider'
          : 'create-provider',
    );
    if (ready?.providerID) target += `&setup_provider=${ready.providerID}`;
    action =
      ready?.channelReady && !ready.verified
        ? zh
          ? '验证渠道连接'
          : 'Verify channel connection'
        : ready?.providerID
          ? t('onboarding.configure_models')
          : t('onboarding.create_channel');
  }
  return (
    <section className="first-key-guide" aria-labelledby="first-key-title">
      <GatewayReadinessNotice canConfigure={superAdmin} />
      <div className="first-key-header">
        <div>
          <div className="first-key-heading">
            <h2 id="first-key-title">
              {zh ? '完成首次成功调用' : 'Make your first successful call'}
            </h2>
            <span>{count} / 4</span>
          </div>
          <p>
            {zh
              ? '渠道连接 → 组织授权 → API Key → 实际调用。进度由当前资源和调用记录判断。'
              : 'Connect a channel, authorize an organization, create a Key, then verify a real call. Progress reflects current resources and call records.'}
          </p>
        </div>
        <div className="first-key-tools">
          {superAdmin && items.length > 0 ? (
            <Select
              aria-label={t('onboarding.organization')}
              value={organization?.id}
              onChange={id => {
                const next = new URLSearchParams(params);
                next.set('setup_org', String(id));
                next.delete('setup_action');
                if (pathname === '/admin/keys') next.set('organization_id', String(id));
                setParams(next, { replace: true });
              }}
              options={items.map(item => ({ value: item.id, label: item.name }))}
            />
          ) : (
            <span>{organization?.name}</span>
          )}
          <Button
            type="text"
            icon={
              <ReloadOutlined
                spin={organizations.isFetching || readiness.isFetching || status.isFetching}
              />
            }
            onClick={refresh}
            aria-label={t('onboarding.refresh')}
          />
        </div>
      </div>
      <ol className="first-key-steps">
        {order.map((name, index) => (
          <li
            key={name}
            className={completed[name] ? 'is-complete' : current === name ? 'is-current' : ''}
            aria-current={current === name ? 'step' : undefined}
          >
            <span className="first-key-step-number">
              {completed[name] ? <CheckOutlined /> : index + 1}
            </span>
            <div>
              <strong>
                {name === 'call'
                  ? zh
                    ? '验证调用'
                    : 'Verify a call'
                  : t(`onboarding.step_${name}`)}
              </strong>
              <span>
                {name === 'channel'
                  ? zh
                    ? '保存模型配置并主动测试连接；保存不等于已验证。'
                    : 'Save model configuration and run Test; saving alone is not verification.'
                  : name === 'call'
                    ? zh
                      ? '主动发出实际请求，确认服务端成功记录。'
                      : 'Send a real request and confirm its successful server record.'
                    : t(`onboarding.hint_${name}`)}
              </span>
            </div>
          </li>
        ))}
      </ol>
      {blocked && (
        <Alert
          type="info"
          showIcon
          message={
            zh
              ? '已分配的公共渠道尚未验证。请联系本实例超级管理员在“上游渠道”测试该渠道；组织管理员只有公共渠道读取权限。'
              : 'This assigned public channel is unverified. Ask your instance super administrator to run Test in Providers & Models; organization administrators have read-only access to public channels.'
          }
        />
      )}
      <div className="first-key-footer">
        <Button
          type="text"
          onClick={() => {
            setPause(true);
            const next = new URLSearchParams(params);
            next.delete('onboarding');
            setParams(next, { replace: true });
          }}
        >
          {zh ? '稍后继续' : 'Continue later'}
        </Button>
        <div>
          <a href="/docs/quickstart#first-key-setup" target="_blank" rel="noreferrer">
            {t('onboarding.help')}
          </a>
          {current === 'call' ? (
            <Button type="primary" onClick={() => setVerify(true)}>
              {zh ? '验证调用（可能产生费用）' : 'Verify a call (may incur charges)'}
            </Button>
          ) : !current ? (
            <Link to="/">
              <Button type="primary">{t('onboarding.finish')}</Button>
            </Link>
          ) : (
            !blocked && (
              <Link to={target}>
                <Button type="primary" icon={<ArrowRightOutlined />}>
                  {action}
                </Button>
              </Link>
            )
          )}
        </div>
      </div>
      {verify && (
        <Suspense fallback={<Skeleton active />}>
          <VerifyCall
            key={organization?.id}
            open={verify}
            onClose={() => setVerify(false)}
            onSuccess={refresh}
          />
        </Suspense>
      )}
    </section>
  );
}
