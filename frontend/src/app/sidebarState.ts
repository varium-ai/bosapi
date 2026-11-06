export const SIDEBAR_STORAGE_KEY = 'bos_sidebar_collapsed';
export const SIDEBAR_MEDIA_QUERY = '(max-width: 991.98px)';

export interface SidebarState {
  desktopCollapsed: boolean;
  isMobile: boolean;
  mobileOpen: boolean;
}

export type SidebarAction =
  { type: 'toggle' } | { type: 'viewport'; isMobile: boolean } | { type: 'close-mobile' };

export function sidebarReducer(state: SidebarState, action: SidebarAction): SidebarState {
  switch (action.type) {
    case 'toggle':
      return state.isMobile
        ? { ...state, mobileOpen: !state.mobileOpen }
        : { ...state, desktopCollapsed: !state.desktopCollapsed };
    case 'viewport':
      return state.isMobile === action.isMobile
        ? state
        : { ...state, isMobile: action.isMobile, mobileOpen: false };
    case 'close-mobile':
      return state.mobileOpen ? { ...state, mobileOpen: false } : state;
  }
}

export function readSidebarPreference(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

export function writeSidebarPreference(collapsed: boolean): void {
  try {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, String(collapsed));
  } catch {
    // Navigation remains usable when browser storage is unavailable.
  }
}
