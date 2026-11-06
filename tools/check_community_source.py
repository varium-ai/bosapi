#!/usr/bin/env python3
"""Check community scope, private-file hygiene, and explicit release decisions."""
from pathlib import Path
import argparse
import json
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--release', action='store_true', help='also require all publication decisions')
parser.add_argument('--frontend-dist', action='store_true', help='also inspect a completed frontend production build')
args = parser.parse_args()
failures = []
excluded = {'.git', 'node_modules', 'dist', '__pycache__', 'coverage'}
admin_modules = {
    'application': ('auto_route', 'report', 'report_processor', 'security_rule', 'security_webhook'),
    'domain': ('auto_route', 'report', 'security_rule', 'security_webhook'),
    'infrastructure': ('auto_routes_gorm', 'report_worker', 'reports_gorm', 'security_rules_gorm', 'security_webhook_worker', 'security_webhooks_gorm'),
    'interfaces': ('auto_routes_http', 'reports_http', 'security_rules_http', 'security_webhooks_http'),
}
excluded_paths = [
    f'backend/internal/admin/{layer}/{name}.go'
    for layer, names in admin_modules.items() for name in names
] + [
    'backend/internal/gateway/infrastructure/tool_rules.go',
    'frontend/src/features/autoroutes', 'frontend/src/features/reports',
    'frontend/src/features/security/SecurityRulesPage.tsx',
    'frontend/src/features/security/SecurityWebhooksPage.tsx',
    'upapi.sh', 'up_test.sh', 'deploy/bluegreen.py', 'deploy/BLUE_GREEN.md',
    'deploy/upload-source.sh', 'deploy/publish-image.sh', 'deploy/install-test-image.sh',
    'deploy/nginx/bosapi-bluegreen-proxy.conf', 'deploy/nginx/bosapi-nginx',
    'backend/internal/platform/updates', 'deploy/image-updater',
    'deploy/tests/test_image_updater.py',
    'frontend/src/features/settings/SystemUpdatesPage.tsx',
    'frontend/src/i18n/systemUpdateMessages.ts',
]
for relative in excluded_paths:
    if (ROOT / relative).exists():
        failures.append(relative + ': excluded implementation or internal deployment tool')
excluded_routes = re.compile(r'''["'`](?:/?admin/(?:auto-routes|security-rules|security-webhooks|reports|usage/weekly-report|system-updates)|/system-updates)(?:["'`/?])''')
obsolete_locale_keys = {'reports', 'autoroutes', 'securityrules', 'securitywebhooks', 'systemupdates', 'queuereport', 'jobqueued', 'privatechannel', 'createprivate'}
obsolete_page_keys = {'reports', 'autoroutes', 'securityrules', 'securitywebhooks', 'systemupdates'}
obsolete_security_keys = re.compile(r'^(?:rules_|webhooks|webhook_|push_|failure_|add_webhook$|add_rule$|sensitivity_note$)')
locale_tokens = re.compile(r'''(?P<comment>//[^\n]*|/\*[\s\S]*?\*/)|(?P<string>'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`)|(?P<name>[A-Za-z_$][\w$]*)|(?P<symbol>[{}:,])''')
stale_descriptions = (
    'enabled global baseline plus enabled organization rules',
    'cached as compiled regular expressions',
    'private channels owned by that organization',
    'private channels belong to one organization',
    'seeds the built-in security rules',
    'missing built-in security rules are inserted',
    'Tool Security Rules page',
    'High sensitivity matches trigger webhooks',
    "enforces each rule's action",
)


def normalized_key(key):
    return key.replace('_', '').replace('-', '').casefold()


def tokens_for(source):
    return [(match.lastgroup, match.group()) for match in locale_tokens.finditer(source) if match.lastgroup != 'comment']


def page_configuration_violations(source):
    """Find removed page identifiers in help, icon, and other object maps."""
    tokens = tokens_for(source)
    return sorted({
        value.strip("'\"`")
        for index, (kind, value) in enumerate(tokens[:-1])
        if kind in {'name', 'string'} and tokens[index + 1][1] == ':'
        and normalized_key(value.strip("'\"`")) in obsolete_page_keys
    })


def locale_violations(source):
    """Inspect property keys and object context, excluding translated values."""
    tokens = tokens_for(source)
    scopes = []
    violations = set()
    for index, (kind, value) in enumerate(tokens):
        if value == '{':
            name = tokens[index - 2][1].strip("'\"`") if index > 1 and tokens[index - 1][1] == ':' else None
            scopes.append(name)
        elif value == '}' and scopes:
            scopes.pop()
        elif kind in {'name', 'string'} and index + 1 < len(tokens) and tokens[index + 1][1] == ':':
            key = value.strip("'\"`")
            if normalized_key(key) in obsolete_locale_keys or ('security' in scopes and obsolete_security_keys.search(key)):
                violations.add(key)
    return sorted(violations)
private_detector = ROOT / 'backend/internal/platform/securityadvanced'
if private_detector.exists():
    failures.append('backend/internal/platform/securityadvanced: private detector must not be exported')
public_evidence = ROOT / 'backend/internal/platform/securityevidence'
for name in ('injection.go', 'injection_patterns.go', 'injection_memo.go', 'prefilter.go',
             'normalize.go', 'decode.go', 'injection_test.go', 'injection_performance_test.go', 'decode_test.go'):
    if (public_evidence / name).exists():
        failures.append('backend/internal/platform/securityevidence/' + name + ': moved private detector file')
corpus = public_evidence / 'testdata/security'
for path in corpus.glob('*.jsonl'):
    if not path.name.startswith('credentials.'):
        failures.append(path.relative_to(ROOT).as_posix() + ': private detector corpus')
entry = ROOT / 'backend/main.go'
template = ROOT / 'backend/open_source/main.go'
if not entry.is_file() or not template.is_file() or entry.read_bytes() != template.read_bytes():
    failures.append('backend/main.go: must match the public open_source/main.go entry point')

for path in ROOT.rglob('*'):
    if not path.is_file() or any(part in excluded for part in path.relative_to(ROOT).parts):
        continue
    relative = path.relative_to(ROOT).as_posix()
    if '.local-private' in path.parts or path.name in {'stack.env', 'image-updater.env', '.env'}:
        failures.append(relative + ': private deployment material must not be published')
    if path.suffix in {'.pem', '.key', '.p12'} or path.name.endswith(('.mmdb', '.mmdb.gz')):
        failures.append(relative + ': private key or separately licensed database')
    if path.suffix not in {'.go', '.tsx', '.ts', '.js', '.jsx', '.mjs', '.cjs', '.sh', '.py', '.sql', '.md'}:
        continue
    source = path.read_text()
    if path.suffix == '.go' and re.search(r'\b(?:securityadvanced|CoreInjectionPatterns|FindInjectionMatches\w*|decodeInjectionViews\w*|instructionLikeRegex)\b', source):
        failures.append(relative + ': private detector import or implementation')
    if path.suffix == '.go' and re.search(r'\b(?:GOCSPX-[A-Za-z0-9_-]{16,}|[0-9]{6,}-[a-z0-9_-]{12,}\.apps\.googleusercontent\.com)\b', source):
        failures.append(relative + ': embedded Google OAuth client material')
    if relative.startswith('tools/') or path.name.endswith(('_test.go', '.test.cjs')):
        continue
    description = ' '.join(source.casefold().split())
    if 'blue_green.md' in description or any(marker.casefold() in description for marker in stale_descriptions):
        failures.append(relative + ': stale excluded-feature description or deleted deployment document')
    if relative.startswith('backend/') and re.search(r'\b(?:security_rules|security_rule_changes|security_webhooks|security_webhook_deliveries)\b', source):
        failures.append(relative + ': dependency on an excluded security table')
    if '/platform/updates"' in source or (relative.startswith('backend/') or relative == 'deploy/stack.sh') and re.search(r'\bBOS_UPDATE_(?:SOCKET|TOKEN|URL|RUN_DIR)\b', source):
        failures.append(relative + ': excluded online-update integration')
    if re.search(r'\b(?:NewAutoRouteHandler|NewSecurityRuleHandler|NewSecurityWebhookHandler|NewReportWorker|NewReportRepository|NewToolRuleEngine|ReportRequest|ReportWorker|NoopReportWorker|EnqueueReport)\b', source):
        failures.append(relative + ': excluded commercial implementation')
    if relative.startswith(('backend/', 'frontend/')) and excluded_routes.search(source):
        failures.append(relative + ': excluded administrative route')
    if relative.startswith('frontend/') and path.suffix != '.md':
        obsolete = page_configuration_violations(source)
        if obsolete:
            failures.append(relative + ': excluded page configuration: ' + ', '.join(obsolete))
    if relative.startswith('frontend/src/i18n/'):
        obsolete = locale_violations(source)
        if obsolete:
            failures.append(relative + ': excluded feature translation keys: ' + ', '.join(obsolete))
    if 'secure-typex/bos-api' in source:
        failures.append(relative + ': internal repository reference')

if args.frontend_dist:
    distribution = ROOT / 'frontend/dist'
    assets = [path for path in distribution.rglob('*') if path.is_file() and path.suffix in {'.js', '.json', '.html'}]
    if not any(path.suffix == '.js' for path in assets):
        failures.append('frontend/dist: run the frontend production build before checking it')
    markers = (
        'Queue Weekly Report', 'Usage Report Jobs', 'Report messages use type=report',
        'Organization private channel', 'Create a private channel',
        'SecurityRulesPage', 'SecurityWebhooksPage', 'AutoRoutesPage', 'ReportsPage', 'SystemUpdatesPage',
        'High sensitivity matches trigger webhooks',
    )
    for path in assets:
        source = path.read_text()
        if (excluded_routes.search(source) or any(marker in source for marker in markers)
                or page_configuration_violations(source) or locale_violations(source)):
            failures.append(path.relative_to(ROOT).as_posix() + ': excluded feature in production frontend')

if args.release:
    status = json.loads((ROOT / '.release-preparation.json').read_text())
    for key, approved in status.items():
        if approved is not True:
            failures.append('release decision pending: ' + key)
    if not (ROOT / 'LICENSE').is_file():
        failures.append('release decision pending: approved LICENSE file')

if failures:
    print('\n'.join(failures), file=sys.stderr)
    sys.exit(1)
print('Community source checks passed' + (' (release decisions included).' if args.release else ' (local preparation only).'))
