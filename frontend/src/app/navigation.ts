export interface NavigationItem {
  path: string;
  label: string;
  icon: string;
  superAdminOnly?: boolean;
}

export interface NavigationGroup {
  key: string;
  label: string;
  items: NavigationItem[];
}

const adminGroups: NavigationGroup[] = [
  {
    key: 'workbench',
    label: 'nav.group_workbench',
    items: [{ path: '/', label: 'nav.overview', icon: 'home' }],
  },
  {
    key: 'resources',
    label: 'nav.group_resources',
    items: [
      { path: '/admin/providers', label: 'nav.providers', icon: 'providers' },
      { path: '/admin/organizations', label: 'nav.organizations', icon: 'team' },
      { path: '/admin/keys', label: 'nav.keys', icon: 'key' },
    ],
  },
  {
    key: 'monitoring',
    label: 'nav.group_monitoring',
    items: [
      { path: '/admin/usage', label: 'nav.usage', icon: 'usage' },
      { path: '/admin/requests', label: 'ux.requests', icon: 'search' },
      { path: '/admin/status', label: 'nav.status', icon: 'status', superAdminOnly: true },
    ],
  },
  {
    key: 'security',
    label: 'nav.group_security',
    items: [
      { path: '/admin/security-events', label: 'nav.security', icon: 'security' },
      { path: '/admin/rate-limits', label: 'nav.rate_limits', icon: 'limits' },
    ],
  },
  {
    key: 'settings',
    label: 'ux.settings',
    items: [
      { path: '/admin/admin-users', label: 'nav.admin_config', icon: 'admins' },
      {
        path: '/admin/announcements',
        label: 'announcements.manage',
        icon: 'message',
      },
      {
        path: '/admin/api-address',
        label: 'nav.api_address',
        icon: 'global',
        superAdminOnly: true,
      },
      {
        path: '/admin/message-config',
        label: 'nav.message_config',
        icon: 'message',
        superAdminOnly: true,
      },
      {
        path: '/admin/usage-reporting',
        label: 'nav.usage_reporting',
        icon: 'usage',
        superAdminOnly: true,
      },
    ],
  },
];

const keyGroups: NavigationGroup[] = [
  {
    key: 'workbench',
    label: 'nav.group_workbench',
    items: [{ path: '/key-portal', label: 'nav.overview', icon: 'home' }],
  },
  {
    key: 'usage',
    label: 'nav.group_key_usage',
    items: [
      { path: '/key-portal/usage', label: 'keys.detail_tab_usage', icon: 'usage' },
      { path: '/key-portal/ips', label: 'keys.detail_tab_ip_usage', icon: 'global' },
    ],
  },
  {
    key: 'diagnostics',
    label: 'nav.group_key_security',
    items: [
      { path: '/key-portal/errors', label: 'keys.detail_tab_errors', icon: 'errors' },
      { path: '/key-portal/security', label: 'keys.detail_tab_security', icon: 'security' },
    ],
  },
];

export function getNavigationGroups(role?: string): NavigationGroup[] {
  if (role !== 'super_admin' && role !== 'org_admin') return keyGroups;
  return adminGroups
    .map(group => ({
      ...group,
      items: group.items.filter(item => !item.superAdminOnly || role === 'super_admin'),
    }))
    .filter(group => group.items.length > 0);
}

export function getSelectedNavigationPath(path: string): string {
  if (path === '/admin/errors' || path === '/admin/request-logs') return '/admin/requests';
  return path.startsWith('/admin/keys/') ? '/admin/keys' : path;
}

// Search names live alongside the navigation they describe.
export const navigationKeywords: Record<string, string[]> = {
  '/': ['overview', '概览', '首页', 'dashboard'],
  '/admin/providers': ['上游', '渠道', '供应商', 'provider', 'upstream', 'models', '模型', '凭证'],
  '/admin/organizations': ['组织', '分配模型', 'organization', 'team', '模型权限', '渠道分配'],
  '/admin/keys': ['API Key', '密钥', '令牌', 'token', 'key', '额度', '有效期'],
  '/admin/requests': [
    '请求',
    '诊断',
    '超时',
    '认证失败',
    'requests',
    'timeout',
    'authentication',
    'diagnostics',
  ],
  '/admin/announcements': ['公告', 'announcement', 'Markdown'],
  '/admin/usage': ['用量', '账单', 'usage', 'billing'],
  '/admin/rate-limits': ['限流', 'rate limits', '429'],
  '/admin/security-events': ['安全', 'security', '审计'],
};
