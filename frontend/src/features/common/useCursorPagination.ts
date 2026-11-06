import { useState } from 'react';

export function useCursorPagination(scope = '') {
  const [state, setState] = useState({ scope, history: [''] });
  if (state.scope !== scope) setState({ scope, history: [''] });
  const history = state.scope === scope ? state.history : [''];
  const cursor = history[history.length - 1];

  return {
    cursor,
    page: history.length,
    history,
    goToPage: (page: number) => {
      if (page < 1 || page > history.length) return;
      setState({ scope, history: history.slice(0, page) });
    },
    previous: () => setState({ scope, history: history.slice(0, Math.max(1, history.length - 1)) }),
    next: (nextCursor?: string) => {
      if (!nextCursor) return;
      setState(current => {
        const entries = current.scope === scope ? current.history : [''];
        // Ignore double clicks and stale responses from an earlier page.
        if (entries[entries.length - 1] !== cursor || entries.includes(nextCursor)) return current;
        return { scope, history: [...entries, nextCursor] };
      });
    },
    reset: () => setState({ scope, history: [''] }),
  };
}
