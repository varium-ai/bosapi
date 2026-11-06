import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import { getInitialLanguage, I18nProvider, loadI18nResources, useI18n } from './i18n';
import App from './app/App';
import { createConsoleTheme } from './theme/consoleTheme';
import './style.css';
import './features/common/buttons.css';
import './features/common/cards.css';
import './features/common/inputs.css';
import './features/common/menus.css';
import './features/common/tables.css';

const consoleTheme = createConsoleTheme();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 30_000,
      gcTime: 10 * 60_000,
    },
  },
});

const RootApp: React.FC = () => {
  const { antdLocale } = useI18n();

  React.useEffect(() => {
    ConfigProvider.config({
      holderRender: children => (
        <ConfigProvider
          locale={antdLocale}
          wave={{ disabled: true }}
          button={{ autoInsertSpace: false }}
          theme={consoleTheme}
        >
          {children}
        </ConfigProvider>
      ),
    });

    return () => ConfigProvider.config({ holderRender: undefined });
  }, [antdLocale]);

  return (
    <ConfigProvider
      locale={antdLocale}
      wave={{ disabled: true }}
      button={{ autoInsertSpace: false }}
      theme={consoleTheme}
    >
      <QueryClientProvider client={queryClient}>
        <BrowserRouter future={{ v7_startTransition: true }}>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </ConfigProvider>
  );
};

async function bootstrap() {
  const initialLanguage = getInitialLanguage();
  const initialI18nResources = await loadI18nResources(initialLanguage);
  // Translation updates can invalidate the entry module; reuse the existing root.
  const root: ReactDOM.Root =
    import.meta.hot?.data.root ??
    ReactDOM.createRoot(document.getElementById('root') as HTMLElement);
  if (import.meta.hot) import.meta.hot.data.root = root;
  root.render(
    <React.StrictMode>
      <I18nProvider initialLanguage={initialLanguage} initialResources={initialI18nResources}>
        <RootApp />
      </I18nProvider>
    </React.StrictMode>,
  );
}

void bootstrap();
