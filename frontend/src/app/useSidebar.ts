import { useCallback, useEffect, useReducer } from 'react';
import {
  readSidebarPreference,
  writeSidebarPreference,
  sidebarReducer,
  SIDEBAR_MEDIA_QUERY,
} from './sidebarState';

export function useSidebar(routeKey: string) {
  const [state, dispatch] = useReducer(sidebarReducer, undefined, () => ({
    desktopCollapsed: readSidebarPreference(),
    isMobile: window.matchMedia(SIDEBAR_MEDIA_QUERY).matches,
    mobileOpen: false,
  }));

  useEffect(() => {
    const media = window.matchMedia(SIDEBAR_MEDIA_QUERY);
    const update = () => dispatch({ type: 'viewport', isMobile: media.matches });
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    writeSidebarPreference(state.desktopCollapsed);
  }, [state.desktopCollapsed]);

  useEffect(() => {
    dispatch({ type: 'close-mobile' });
  }, [routeKey]);

  const toggle = useCallback(() => dispatch({ type: 'toggle' }), []);
  const closeMobile = useCallback(() => dispatch({ type: 'close-mobile' }), []);

  return {
    ...state,
    expanded: state.isMobile ? state.mobileOpen : !state.desktopCollapsed,
    toggle,
    closeMobile,
  };
}
