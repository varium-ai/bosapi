// This markup comes from our static HTML, before React replaces the root.
const markup = document.querySelector('[data-startup-shell]')?.outerHTML;

export function AppLoadingShell() {
  return markup ? (
    <div aria-busy="true" dangerouslySetInnerHTML={{ __html: markup }} />
  ) : (
    <div aria-busy="true" style={{ minHeight: '100vh', background: '#f7f7f7' }} />
  );
}
