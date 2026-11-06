// All configuration error surfaces share one persistent owner.
export function reportConfigurationError(error: any) {
  if (error?.configurationFeedbackHandled) return;
  if (error && typeof error === 'object') error.configurationFeedbackHandled = true;
  const detail =
    typeof error === 'string'
      ? { response: { data: { error: { code: error } } } }
      : error?.config || error?.response
        ? error
        : { response: { data: { error: { code: 'unknown_error' } } } };
  window.dispatchEvent(new CustomEvent('bos:configuration-error', { detail }));
}

export function clearConfigurationError() {
  window.dispatchEvent(new CustomEvent('bos:configuration-clear'));
}
