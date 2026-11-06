import React, { lazy, Suspense } from 'react';
import { Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { lazyRoute, preloadRoute } from './lazyRoute';
import { AppLoadingShell } from './AppLoadingShell';
import { useI18n } from '../i18n';
const LoginPage = lazy(() =>
  import('../features/auth/LoginPage').then(m => ({ default: m.LoginPage })),
);

const AppLayout = lazy(() => import('./Layout').then(m => ({ default: m.AppLayout })));
const DocsPage = lazy(() => import('../features/docs/DocsPage'));
const HomePage = lazyRoute(
  () => import('../features/home/HomePage').then(m => ({ default: m.HomePage })),
  ['/'],
);
const OrganizationsPage = lazyRoute(
  () =>
    import('../features/organizations/OrganizationsPage').then(m => ({
      default: m.OrganizationsPage,
    })),
  ['/admin/organizations'],
);
const KeysPage = lazyRoute(
  () => import('../features/keys/KeysPage').then(m => ({ default: m.KeysPage })),
  ['/admin/keys'],
);
const ProvidersPage = lazyRoute(
  () => import('../features/providers/ProvidersPage').then(m => ({ default: m.ProvidersPage })),
  ['/admin/providers'],
);
const UsagePage = lazyRoute(
  () => import('../features/usage/UsagePage').then(m => ({ default: m.UsagePage })),
  ['/admin/usage', '/admin/requests'],
);
const ErrorsPage = lazyRoute(
  () => import('../features/errors/ErrorsPage').then(m => ({ default: m.ErrorsPage })),
  ['/admin/errors'],
);
const SecurityEventsPage = lazyRoute(
  () =>
    import('../features/security/SecurityEventsPage').then(m => ({
      default: m.SecurityEventsPage,
    })),
  ['/admin/security-events'],
);
const StatusPage = lazyRoute(
  () => import('../features/status/StatusPage').then(m => ({ default: m.StatusPage })),
  ['/admin/status'],
);
const RateLimitsPage = lazyRoute(
  () => import('../features/ratelimits/RateLimitsPage').then(m => ({ default: m.RateLimitsPage })),
  ['/admin/rate-limits'],
);
const AdminUsersPage = lazyRoute(
  () => import('../features/adminusers/AdminUsersPage').then(m => ({ default: m.AdminUsersPage })),
  ['/admin/admin-users'],
);
const MessageNotificationConfigPage = lazyRoute(
  () =>
    import('../features/adminusers/MessageNotificationConfigPage').then(m => ({
      default: m.MessageNotificationConfigPage,
    })),
  ['/admin/message-config'],
);
const UsageReportingPage = lazyRoute(
  () =>
    import('../features/settings/UsageReportingPage').then(m => ({
      default: m.UsageReportingPage,
    })),
  ['/admin/usage-reporting'],
);
const GatewaySettingsPage = lazyRoute(
  () =>
    import('../features/settings/GatewaySettingsPage').then(m => ({
      default: m.GatewaySettingsPage,
    })),
  ['/admin/api-address'],
);
const AnnouncementsPage = lazyRoute(
  () =>
    import('../features/announcements/AnnouncementsPage').then(m => ({
      default: m.AnnouncementsPage,
    })),
  ['/admin/announcements'],
);
const KeyDetailPage = lazyRoute(
  () => import('../features/keys/KeyDetailPage').then(m => ({ default: m.KeyDetailPage })),
  ['/admin/keys/:keyId'],
);
const KeyPortalPage = lazyRoute(
  () => import('../features/keys/KeyPortalPage').then(m => ({ default: m.KeyPortalPage })),
  ['/key-portal/*'],
);
const RequestLogsPage = lazyRoute(
  () =>
    import('../features/requestlogs/RequestLogsPage').then(m => ({ default: m.RequestLogsPage })),
  ['/admin/request-logs'],
);
const ForbiddenPage = lazy(() => import('./ForbiddenPage'));

const ProtectedRoute: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const token = localStorage.getItem('auth_token');
  const keyPortalToken = localStorage.getItem('key_portal_token');
  if (!token && !keyPortalToken) {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
};

const RoleGuard: React.FC<{ allowedRoles: string[]; children: React.ReactNode }> = ({
  allowedRoles,
  children,
}) => {
  let role = '';
  try {
    const raw = localStorage.getItem('user_info');
    if (raw) {
      role = JSON.parse(raw)?.role || '';
    }
  } catch {
    // Invalid stored user information keeps the role guard closed.
  }

  if (!allowedRoles.includes(role)) {
    return <ForbiddenPage />;
  }

  return <>{children}</>;
};

const KeyPortalGuard: React.FC<{ children: React.ReactNode }> = ({ children }) =>
  localStorage.getItem('key_portal_token') ? <>{children}</> : <Navigate to="/login" replace />;

export const AppRoutes: React.FC = () => {
  const { t } = useI18n();
  const { pathname } = useLocation();
  if (localStorage.getItem('auth_token') || localStorage.getItem('key_portal_token')) {
    preloadRoute(pathname);
  }
  return (
    <Routes>
      <Route path="/docs" element={<Navigate to="/docs/quickstart" replace />} />
      <Route
        path="/docs/:slug"
        element={
          <Suspense
            fallback={
              <div role="status" style={{ padding: 24 }}>
                {t('docs.loading')}
              </div>
            }
          >
            <DocsPage />
          </Suspense>
        }
      />
      <Route
        path="/login"
        element={
          <Suspense
            fallback={
              <div role="status" style={{ padding: 24 }}>
                {t('common.loading')}
              </div>
            }
          >
            <LoginPage />
          </Suspense>
        }
      />
      <Route
        path="/"
        element={
          <ProtectedRoute>
            <Suspense fallback={<AppLoadingShell />}>
              <AppLayout />
            </Suspense>
          </ProtectedRoute>
        }
      >
        <Route
          path="key-portal/*"
          element={
            <KeyPortalGuard>
              <KeyPortalPage />
            </KeyPortalGuard>
          }
        />
        <Route index element={<HomePage />} />
        {/* Admin Console Routes */}
        <Route
          path="admin/usage-reporting"
          element={
            <RoleGuard allowedRoles={['super_admin']}>
              <UsageReportingPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/api-address"
          element={
            <RoleGuard allowedRoles={['super_admin']}>
              <GatewaySettingsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/organizations"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <OrganizationsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/keys"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <KeysPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/keys/:keyId"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <KeyDetailPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/providers"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <ProvidersPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/rate-limits"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <RateLimitsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/usage"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <UsagePage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/requests"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <UsagePage recordsOnly />
            </RoleGuard>
          }
        />
        <Route
          path="admin/errors"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <ErrorsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/request-logs"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <RequestLogsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/security-events"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <SecurityEventsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/admin-users"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <AdminUsersPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/announcements"
          element={
            <RoleGuard allowedRoles={['super_admin', 'org_admin']}>
              <AnnouncementsPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/message-config"
          element={
            <RoleGuard allowedRoles={['super_admin']}>
              <MessageNotificationConfigPage />
            </RoleGuard>
          }
        />
        <Route
          path="admin/status"
          element={
            <RoleGuard allowedRoles={['super_admin']}>
              <StatusPage />
            </RoleGuard>
          }
        />
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
};
