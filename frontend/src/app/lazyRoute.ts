import { lazy, type ComponentType } from 'react';
import { matchPath } from 'react-router-dom';

const routes: { paths: string[]; preload: () => Promise<unknown> }[] = [];

export function lazyRoute<T extends ComponentType<any>>(
  load: () => Promise<{ default: T }>,
  paths: string[],
) {
  let pending: ReturnType<typeof load> | undefined;
  const preload = () => (pending ??= load());
  routes.push({ paths, preload });
  return lazy(preload);
}

export function preloadRoute(pathname: string) {
  // Start the matching page before the lazy layout renders its Outlet.
  for (const route of routes) {
    if (route.paths.some(path => matchPath(path, pathname))) {
      void route.preload().catch(() => {});
    }
  }
}
