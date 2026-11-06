import { useEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { positiveID } from './firstKey';

// Consume explicit guide actions once. The rest of the URL preserves progress
// across refreshes and leaves the regular page's create/edit actions intact.
export function useFirstKeyAction(
  actions: string[],
  enabled: boolean,
  onAction: (action: string, organizationID?: number, providerID?: number) => void,
) {
  const [params, setParams] = useSearchParams();
  const handler = useRef(onAction);
  const consumed = useRef<string>();
  handler.current = onAction;
  const allowed = actions.join(',');
  useEffect(() => {
    const action = params.get('setup_action');
    if (!action) consumed.current = undefined;
    if (
      !enabled ||
      params.get('onboarding') !== 'first-key' ||
      !action ||
      !allowed.split(',').includes(action)
    )
      return;
    if (consumed.current === params.toString()) return;
    consumed.current = params.toString();
    const next = new URLSearchParams(params);
    next.delete('setup_action');
    setParams(next, { replace: true });
    handler.current(
      action,
      positiveID(params.get('setup_org')),
      positiveID(params.get('setup_provider')),
    );
  }, [allowed, enabled, params, setParams]);
  return params.get('onboarding') === 'first-key';
}
