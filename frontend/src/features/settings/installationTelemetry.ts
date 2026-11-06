// The settings preview renders only this installation-only contract. Never render
// arbitrary API objects or a reused local-usage/status response as telemetry.
export const INSTALLATION_REPORT_FIELDS = [
  'schema_version',
  'report_id',
  'installation_id',
  'collection_epoch',
  'report_kind',
  'generated_at',
  'business_date',
  'timezone',
  'app_version',
  'os',
  'arch',
] as const;

export interface InstallationReport {
  schema_version: 2;
  report_id: string;
  installation_id: string;
  collection_epoch: string;
  report_kind: 'installation' | 'daily';
  generated_at: string;
  business_date?: string;
  timezone?: 'UTC';
  app_version: string;
  os: string;
  arch: string;
}
export interface InstallationPreview {
  report: InstallationReport;
  template: boolean;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const version = /^(unknown|v?[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}|[0-9a-f]{40}|[0-9a-f]{64})$/;
const operatingSystems = new Set([
  'linux',
  'darwin',
  'windows',
  'freebsd',
  'openbsd',
  'netbsd',
  'dragonfly',
  'solaris',
  'illumos',
  'aix',
  'android',
  'ios',
  'plan9',
  'js',
  'wasip1',
]);
const architectures = new Set([
  '386',
  'amd64',
  'arm',
  'arm64',
  'loong64',
  'mips',
  'mipsle',
  'mips64',
  'mips64le',
  'ppc64',
  'ppc64le',
  'riscv64',
  's390x',
  'wasm',
]);

export function parseInstallationPreview(value: unknown): InstallationPreview {
  const invalid = () => {
    throw new Error('Invalid installation telemetry preview');
  };
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid();
  const envelope = value as Record<string, unknown>;
  if (
    typeof envelope.template !== 'boolean' ||
    !envelope.report ||
    typeof envelope.report !== 'object' ||
    Array.isArray(envelope.report)
  )
    return invalid();
  const report = envelope.report as Record<string, unknown>;
  if (
    Object.keys(report).some(
      key => !(INSTALLATION_REPORT_FIELDS as readonly string[]).includes(key),
    )
  )
    return invalid();
  if (
    report.schema_version !== 2 ||
    typeof report.report_id !== 'string' ||
    !uuid.test(report.report_id)
  )
    return invalid();
  for (const key of ['installation_id', 'collection_epoch']) {
    const id = report[key];
    if (typeof id !== 'string' || !(uuid.test(id) || (envelope.template && id === 'not-created')))
      return invalid();
  }
  if (
    typeof report.app_version !== 'string' ||
    !version.test(report.app_version) ||
    typeof report.os !== 'string' ||
    !operatingSystems.has(report.os) ||
    typeof report.arch !== 'string' ||
    !architectures.has(report.arch) ||
    typeof report.generated_at !== 'string' ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/.test(report.generated_at) ||
    !Number.isFinite(Date.parse(report.generated_at))
  )
    return invalid();
  if (report.report_kind === 'installation') {
    if ('business_date' in report || 'timezone' in report) return invalid();
  } else if (report.report_kind === 'daily') {
    const date = new Date(report.generated_at).toISOString().slice(0, 10);
    if (report.business_date !== date || report.timezone !== 'UTC') return invalid();
  } else return invalid();
  // Copy just the validated report and boolean; never retain/render arbitrary explanations.
  return { report: { ...report } as unknown as InstallationReport, template: envelope.template };
}
