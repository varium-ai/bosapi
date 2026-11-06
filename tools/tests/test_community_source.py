"""Protect community boundaries while allowing authentication and telemetry."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


CHECKER = Path(__file__).resolve().parents[1] / 'check_community_source.py'


class CommunitySourceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.write('backend/main.go', 'package main\nfunc main() {}\n')
        self.write('backend/open_source/main.go', 'package main\nfunc main() {}\n')
        self.write('frontend/dist/assets/app.js', 'console.log("community");')
        (self.root / 'tools').mkdir()
        shutil.copy2(CHECKER, self.root / 'tools/check_community_source.py')

    def write(self, relative, text):
        target = self.root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        return target

    def check(self, *arguments):
        return subprocess.run(
            [sys.executable, str(self.root / 'tools/check_community_source.py'), *arguments],
            capture_output=True, text=True,
        )

    def test_authentication_webhooks_and_installation_telemetry_are_allowed(self):
        self.write('frontend/src/settings.ts', "const route = '/admin/usage-reporting';")
        self.write('frontend/src/i18n/locales/en-US.ts', "export const messages = {\n  message_config: { webhook_note: 'Administrator OTP delivery' },\n};")
        self.assertEqual(self.check('--frontend-dist').returncode, 0)

    def test_scope_exclusions_and_read_only_security_metadata_are_allowed(self):
        self.write('COMMUNITY_SCOPE.md', 'Private channels, custom security rules, and security-event Webhooks are excluded.\n')
        self.write('frontend/src/i18n/locales/en-US.ts', "export const messages = { security: { rule_scope: 'Rule scope', sensitivity_high: 'High' }, message_config: { webhook_note: 'OTP delivery' } };")
        self.write('frontend/dist/assets/app.js', 'const messages={security:{rule_scope:"Rule scope",sensitivity_high:"High"},message_config:{webhook_note:"OTP delivery"}};')
        self.assertEqual(self.check('--frontend-dist').returncode, 0)

    def test_embedded_google_oauth_material_is_rejected(self):
        for value in ('GOCSPX-' + 'x' * 24,
                      '123456789-' + 'a' * 24 + '.apps.googleusercontent.com'):
            with self.subTest(value=value):
                self.write('backend/oauth.go', f'package main\nconst client = "{value}"\n')
                result = self.check()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('embedded Google OAuth client material', result.stderr)

    def test_report_ports_and_noop_workers_are_rejected(self):
        for symbol in ('ReportRequest', 'ReportWorker', 'NoopReportWorker', 'EnqueueReport'):
            with self.subTest(symbol=symbol):
                self.write('backend/internal/gateway/domain/ports.go', f'package domain\ntype {symbol} struct{{}}\n')
                result = self.check()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('excluded commercial implementation', result.stderr)

    def test_excluded_routes_are_rejected_in_application_and_build_helpers(self):
        for file in ('frontend/src/page.tsx', 'frontend/build-routes.mjs'):
            for route in ('reports', 'auto-routes', 'security-rules', 'security-webhooks', 'usage/weekly-report', 'system-updates'):
                for prefix in ('/admin/', 'admin/'):
                    with self.subTest(file=file, route=route, prefix=prefix):
                        target = self.write(file, f"const route = '{prefix}{route}';")
                        result = self.check()
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn('excluded administrative route', result.stderr)
                        target.unlink()

    def test_unused_commercial_translations_are_rejected(self):
        for text in (
            "export const messages = { reports: { title: 'Report jobs' } };",
            "export const messages = {\n  reports: { title: 'Report jobs' },\n};",
            "export const messages = {\n  security: {\n    push_pending: 'Pending',\n  },\n};",
            "export const messages = {\n  onboarding: {\n    create_private: 'Private channel',\n  },\n};",
        ):
            with self.subTest(text=text):
                self.write('frontend/src/i18n/locales/en-US.ts', text)
                self.assertNotEqual(self.check().returncode, 0)

    def test_auto_route_naming_variants_are_rejected_in_locales_and_bundles(self):
        for key in ('autoroutes', 'auto_routes', 'autoRoutes', 'auto-routes'):
            for quoted in (False, True):
                if '-' in key and not quoted:
                    continue
                name = f'"{key}"' if quoted else key
                with self.subTest(key=key, quoted=quoted):
                    text = f'const messages={{{name}:{{title:"Model Routes"}}}};'
                    self.write('frontend/src/i18n/locales/en-US.ts', text)
                    self.assertNotEqual(self.check().returncode, 0)
                    (self.root / 'frontend/src/i18n/locales/en-US.ts').unlink()
                    self.write('frontend/dist/assets/app.js', text)
                    result = self.check('--frontend-dist')
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('excluded feature in production frontend', result.stderr)
                    self.write('frontend/dist/assets/app.js', 'console.log("community");')

    def test_dead_help_and_icon_configuration_is_rejected(self):
        for key in ('auto-routes', 'security-rules', 'security-webhooks', 'reports', 'system_updates'):
            with self.subTest(key=key):
                self.write('frontend/src/help.tsx', f'const pageGuides={{"{key}":"quickstart"}};')
                self.assertNotEqual(self.check().returncode, 0)
                (self.root / 'frontend/src/help.tsx').unlink()
                self.write('frontend/dist/assets/app.js', f'const icons={{"{key}":renderIcon()}};')
                self.assertNotEqual(self.check('--frontend-dist').returncode, 0)
                self.write('frontend/dist/assets/app.js', 'console.log("community");')

    def test_security_webhook_claim_is_rejected_in_every_locale_and_bundle(self):
        for language, message in (
            ('en-US', 'High sensitivity matches trigger webhooks'),
            ('zh-CN', '\u9ad8\u654f\u611f\u89c4\u5219\u547d\u4e2d\u4f1a\u89e6\u53d1 Webhook \u63a8\u9001'),
            ('ja-JP', 'Webhook'), ('de-DE', 'Webhooks'), ('es-ES', 'webhooks'),
        ):
            with self.subTest(language=language):
                text = f'const messages={{security:{{sensitivity_note:"{message}"}}}};'
                target = self.write(f'frontend/src/i18n/locales/{language}.ts', text)
                self.assertNotEqual(self.check().returncode, 0)
                target.unlink()
                self.write('frontend/dist/assets/app.js', text)
                self.assertNotEqual(self.check('--frontend-dist').returncode, 0)
                self.write('frontend/dist/assets/app.js', 'console.log("community");')

    def test_stale_docs_and_comment_references_are_rejected(self):
        for file, text in (
            ('backend/migrations/README.md', 'Private channels belong to one\norganization.'),
            ('backend/internal/gateway/domain/README.md', 'The effective policy is cached as compiled regular expressions.'),
            ('deploy/STATIC_WEB.md', 'See [deployment](BLUE_GREEN.md).'),
            ('deploy/reload.sh', '# See deploy/BLUE_GREEN.md\n'),
        ):
            with self.subTest(file=file):
                target = self.write(file, text)
                result = self.check()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('stale excluded-feature description', result.stderr)
                target.unlink()

    def test_reintroduced_modules_are_rejected_even_without_known_symbols(self):
        for relative in ('backend/internal/admin/interfaces/reports_http.go', 'frontend/src/features/reports/Page.tsx', 'upapi.sh', 'deploy/nginx/bosapi-nginx', 'backend/internal/platform/updates/http.go', 'deploy/image-updater/agent.py', 'frontend/src/i18n/systemUpdateMessages.ts'):
            with self.subTest(relative=relative):
                target = self.write(relative, '// placeholder\n')
                self.assertNotEqual(self.check().returncode, 0)
                target.unlink()

    def test_online_update_bridges_and_relative_backend_routes_are_rejected(self):
        for source in ('const socket = "BOS_UPDATE_SOCKET"', 'import "bosapi/backend/internal/platform/updates"', 'const route = "/system-updates"'):
            with self.subTest(source=source):
                self.write('backend/router.go', source)
                self.assertNotEqual(self.check().returncode, 0)

    def test_production_build_is_checked_and_must_exist(self):
        for text in ('const label = "Queue Weekly Report";', 'const route = "/admin/reports";'):
            with self.subTest(text=text):
                self.write('frontend/dist/assets/app.js', text)
                result = self.check('--frontend-dist')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('excluded feature in production frontend', result.stderr)
        (self.root / 'frontend/dist/assets/app.js').unlink()
        self.assertNotEqual(self.check('--frontend-dist').returncode, 0)


if __name__ == '__main__':
    unittest.main()
