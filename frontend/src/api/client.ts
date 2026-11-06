import { clearConfigurationError } from '../features/diagnostics/reportConfigurationError';
import axios from 'axios';
import { message } from 'antd';

declare module 'axios' {
  export interface AxiosRequestConfig {
    silent?: boolean;
    skipGlobalErrorMessage?: boolean;
    _retry?: boolean;
    _configurationPath?: string;
  }
}

export const API_BASE_URL = (import.meta as any).env?.VITE_API_BASE_URL || '/api/v1';

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
});

let isRefreshing = false;
let failedQueue: Array<{
  resolve: (value?: any) => void;
  reject: (reason?: any) => void;
}> = [];

let isKeyPortalRefreshing = false;
let failedKeyPortalQueue: Array<{
  resolve: (value?: any) => void;
  reject: (reason?: any) => void;
}> = [];

const processQueue = (error: any = null) => {
  failedQueue.forEach(prom => {
    if (error) {
      prom.reject(error);
    } else {
      prom.resolve();
    }
  });
  failedQueue = [];
};

const processKeyPortalQueue = (error: any = null) => {
  failedKeyPortalQueue.forEach(prom => {
    if (error) {
      prom.reject(error);
    } else {
      prom.resolve();
    }
  });
  failedKeyPortalQueue = [];
};

apiClient.interceptors.request.use(
  config => {
    config._configurationPath ??= window.location.pathname;
    const isKeyPortalRequest = config.url?.includes('/key-portal');
    const token = isKeyPortalRequest
      ? localStorage.getItem('key_portal_token')
      : localStorage.getItem('auth_token');
    if (token && config.headers) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  error => Promise.reject(error),
);

apiClient.interceptors.response.use(
  response => {
    if (
      /\/admin\/(providers|organizations|keys)(?:\/|$)/.test(response.config.url || '') &&
      ['post', 'put', 'patch', 'delete'].includes(response.config.method?.toLowerCase() || '') &&
      response.config._configurationPath === window.location.pathname
    )
      clearConfigurationError();
    return response;
  },
  async error => {
    // Aborted query requests are lifecycle events, not API failures.
    if (axios.isCancel(error)) return Promise.reject(error);

    const originalRequest = error.config;

    if (
      error.response?.status === 401 &&
      originalRequest?._retry &&
      originalRequest.url?.includes('/key-portal')
    ) {
      clearKeyPortalLocal();
      if (window.location.pathname !== '/login') window.location.href = '/login';
      return Promise.reject(error);
    }

    // Handle 401 Unauthorized
    if (error.response?.status === 401 && !originalRequest._retry) {
      if (originalRequest.url?.includes('/key-portal')) {
        originalRequest._retry = true;
        if (isKeyPortalRefreshing) {
          return new Promise((resolve, reject) => {
            failedKeyPortalQueue.push({ resolve, reject });
          })
            .then(() => apiClient(originalRequest))
            .catch(err => Promise.reject(err));
        }

        isKeyPortalRefreshing = true;
        try {
          const response = await axios.post(
            `${API_BASE_URL}/auth/key-refresh`,
            {},
            { withCredentials: true, timeout: 30000 },
          );
          const session = response.data?.data;
          if (!session?.access_token) throw new Error('invalid key session refresh response');
          localStorage.setItem('key_portal_token', session.access_token);
          if (session.key) {
            localStorage.setItem('key_portal_info', JSON.stringify(session.key));
            localStorage.setItem(
              'user_info',
              JSON.stringify({
                ...session.key,
                role: 'key_viewer',
                email: session.key.name || 'API Key',
              }),
            );
          }
          processKeyPortalQueue(null);
          return apiClient(originalRequest);
        } catch (refreshError) {
          processKeyPortalQueue(refreshError);
          clearKeyPortalLocal();
          if (window.location.pathname !== '/login') window.location.href = '/login';
          return Promise.reject(refreshError);
        } finally {
          isKeyPortalRefreshing = false;
        }
      }
      // Credential and MFA failures belong to the sign-in flow, not session refresh.
      if (
        originalRequest.url?.includes('/auth/login') ||
        originalRequest.url?.includes('/auth/key-login') ||
        originalRequest.url?.includes('/auth/mfa/verify') ||
        originalRequest.url?.includes('/auth/refresh')
      ) {
        const errorMsg =
          error.response?.data?.error?.message ||
          error.response?.data?.message ||
          'Authentication failed';
        message.error(errorMsg);
        return Promise.reject(error);
      }

      const refreshToken = localStorage.getItem('refresh_token');
      if (!refreshToken) {
        logoutLocal();
        return Promise.reject(error);
      }

      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          failedQueue.push({ resolve, reject });
        })
          .then(() => apiClient(originalRequest))
          .catch(err => Promise.reject(err));
      }

      originalRequest._retry = true;
      isRefreshing = true;

      try {
        const res = await axios.post(`${API_BASE_URL}/auth/refresh`, {
          refresh_token: refreshToken,
        });
        const pair = res.data?.data;
        if (pair?.access_token) {
          localStorage.setItem('auth_token', pair.access_token);
          if (pair.refresh_token) {
            localStorage.setItem('refresh_token', pair.refresh_token);
          }
          apiClient.defaults.headers.common.Authorization = `Bearer ${pair.access_token}`;
          processQueue(null);
          return apiClient(originalRequest);
        }
        logoutLocal();
        return Promise.reject(error);
      } catch (refreshErr) {
        processQueue(refreshErr);
        logoutLocal();
        return Promise.reject(refreshErr);
      } finally {
        isRefreshing = false;
      }
    }

    // Suppress global toast if the request explicitly opted out (e.g. background lookups in ResourceName)
    const isSilent =
      originalRequest?.silent ||
      originalRequest?.skipGlobalErrorMessage ||
      originalRequest?.headers?.['X-Silent-Error'] === 'true';

    if (isSilent) {
      return Promise.reject(error);
    }

    // Configuration failures have one persistent, sanitized UI owner.
    if (
      /\/admin\/(providers|organizations|keys)(?:\/|$)/.test(originalRequest?.url || '') &&
      typeof window.dispatchEvent === 'function'
    ) {
      error.configurationFeedbackHandled = true;
      window.dispatchEvent(new CustomEvent('bos:configuration-error', { detail: error }));
      return Promise.reject(error);
    }

    // Extract standardized error message from backend error envelope: { error: { code, message, details } }
    const backendMessage =
      error.response?.data?.error?.message ||
      error.response?.data?.message ||
      (error.response?.data?.error?.code ? `Error: ${error.response.data.error.code}` : null);

    if (backendMessage && error.response?.status !== 401) {
      message.error({ content: backendMessage, key: backendMessage });
    } else if (error.message && error.response?.status !== 401) {
      message.error({ content: error.message, key: error.message });
    }

    return Promise.reject(error);
  },
);

export const logoutLocal = () => {
  localStorage.removeItem('auth_token');
  localStorage.removeItem('refresh_token');
  localStorage.removeItem('user_info');
  localStorage.removeItem('key_portal_token');
  localStorage.removeItem('key_portal_info');
  if (window.location.pathname !== '/login') {
    window.location.href = '/login';
  }
};

const clearKeyPortalLocal = () => {
  localStorage.removeItem('key_portal_token');
  localStorage.removeItem('key_portal_info');
  const userInfo = localStorage.getItem('user_info');
  if (!userInfo) return;
  try {
    if (JSON.parse(userInfo)?.role === 'key_viewer') localStorage.removeItem('user_info');
  } catch {
    localStorage.removeItem('user_info');
  }
};

export const logoutKeyPortal = async () => {
  try {
    await axios.post(
      `${API_BASE_URL}/auth/key-logout`,
      {},
      { withCredentials: true, timeout: 30000 },
    );
  } catch {
    // Local sign-out still succeeds if the session cookie is already gone.
  } finally {
    clearKeyPortalLocal();
    // A full navigation clears identity-independent Query caches before another key signs in.
    window.location.href = '/login';
  }
};

export const logout = async () => {
  try {
    await apiClient.post('/auth/logout');
  } catch {
    // Ignore logout network errors
  } finally {
    logoutLocal();
  }
};

export default apiClient;
