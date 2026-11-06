"""Run static routing and proxy acceptance against an isolated real Nginx."""

import gzip
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import unittest


REPOSITORY = Path(__file__).resolve().parents[2]
NGINX = os.environ.get("NGINX_BIN") or shutil.which("nginx")
SPEC = importlib.util.spec_from_file_location("publish_web", REPOSITORY / "deploy/publish-web.py")
WEB = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WEB)


class Backend(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get("Upgrade") == "websocket":
            self.send_response(101)
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.end_headers()
            return
        body = json.dumps({"uri": self.path, "method": self.command,
                           "host": self.headers.get("Host"),
                           "forwarded_proto": self.headers.get("X-Forwarded-Proto")}).encode()
        self.send_response(418)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_POST = do_GET

    def log_message(self, *args):
        pass


@unittest.skipUnless(NGINX, "Set NGINX_BIN to run real HTTP integration")
class NginxTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix="bosapi-nginx-test-")
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.base = Path(cls.temporary.name).resolve()
        cls.root = cls.base / "host-web"
        cls.root.mkdir()
        cls.source = cls.base / "dist"
        shutil.copytree(REPOSITORY / "frontend/public", cls.source)
        (cls.source / "assets").mkdir()
        cls.original = b"console.log('original chunk');\n" * 100
        (cls.source / "assets/app-abcdefgh.js").write_bytes(cls.original)
        (cls.source / "assets/app-abcdefgh.js.gz").write_bytes(gzip.compress(cls.original, mtime=0))
        (cls.source / "assets/custom.js").write_text("unversioned")
        (cls.source / "index.html").write_text('<html><script src="/assets/app-abcdefgh.js"></script></html>')
        with WEB.publication_lock(cls.root):
            cls.first = WEB.publish(cls.root, cls.source, "fixture-image")
        cls.backend = ThreadingHTTPServer(("127.0.0.1", 0), Backend)
        cls.addClassCleanup(cls.backend.server_close)
        worker = threading.Thread(target=cls.backend.serve_forever, daemon=True)
        worker.start()
        cls.addClassCleanup(lambda: worker.join(timeout=5))
        cls.addClassCleanup(cls.backend.shutdown)
        with socket.socket() as reserve:
            reserve.bind(("127.0.0.1", 0))
            cls.port = reserve.getsockname()[1]
        templates = REPOSITORY / "deploy/nginx"
        proxy = (templates / "bosapi-proxy.conf").read_text().replace(
            "127.0.0.1:18080", f"127.0.0.1:{cls.backend.server_port}")
        proxy_file = cls.base / "proxy.conf"
        proxy_file.write_text(proxy)
        static = (templates / "bosapi-static.conf").read_text().replace(
            "/var/www/bosapi", str(cls.root)).replace(
            "/etc/nginx/snippets/bosapi-proxy.conf", str(proxy_file))
        config = cls.base / "nginx.conf"
        config.write_text(
            f"pid {cls.base}/nginx.pid;\nerror_log stderr;\nevents {{}}\nhttp {{\n"
            "access_log off;\ntypes { text/html html; application/javascript js; text/css css; "
            "image/svg+xml svg; text/plain txt md; application/json json; }\n"
            + (templates / "bosapi-connection.conf").read_text()
            + f"\nserver {{ listen 127.0.0.1:{cls.port}; server_name localhost;\n"
            + static + "\n}\n}\n")
        subprocess.run([NGINX, "-t", "-p", str(cls.base) + "/", "-c", str(config)],
                       check=True, capture_output=True)
        cls.log = (cls.base / "nginx.log").open("w+")
        cls.addClassCleanup(cls.log.close)
        cls.process = subprocess.Popen(
            [NGINX, "-p", str(cls.base) + "/", "-c", str(config), "-g", "daemon off;"],
            stdout=cls.log, stderr=cls.log)
        cls.addClassCleanup(cls.stop_nginx)
        # Connection retries are bounded; each failed connection yields through Event.wait.
        for _ in range(100):
            try:
                with socket.create_connection(("127.0.0.1", cls.port), timeout=0.1):
                    break
            except OSError:
                if cls.process.poll() is not None:
                    raise RuntimeError((cls.base / "nginx.log").read_text())
                threading.Event().wait(0.02)
        else:
            raise RuntimeError("isolated nginx did not become ready")

    @classmethod
    def stop_nginx(cls):
        if cls.process.poll() is None:
            cls.process.send_signal(signal.SIGQUIT)
            cls.process.wait(timeout=10)

    def request(self, path, method="GET", headers=None):
        connection = HTTPConnection("127.0.0.1", self.port, timeout=5)
        try:
            connection.request(method, path, headers=headers or {})
            response = connection.getresponse()
            body = b"" if response.status == 101 else response.read()
            return response.status, dict(response.getheaders()), body
        finally:
            connection.close()

    def test_spa_refresh_and_docs_directory(self):
        for path in ["/", "/login", "/admin/keys", "/key-portal/example", "/docs", "/docs/getting-started", "/models-page"]:
            with self.subTest(path=path):
                status, headers, body = self.request(path)
                self.assertEqual(status, 200)
                self.assertIn(b"<html>", body)
                self.assertEqual(headers["Cache-Control"], "no-cache, no-store, must-revalidate")

    def test_assets_mime_cache_compression_and_missing_files(self):
        status, headers, body = self.request("/assets/app-abcdefgh.js", headers={"Accept-Encoding": "gzip"})
        self.assertEqual(status, 200)
        self.assertEqual(headers["Content-Type"], "application/javascript")
        self.assertEqual(headers["Content-Encoding"], "gzip")
        self.assertEqual(headers["Vary"], "Accept-Encoding")
        self.assertEqual(headers["Cache-Control"], "public, max-age=31536000, immutable")
        self.assertEqual(gzip.decompress(body), self.original)
        self.assertLess(len(body), len(self.original))
        self.assertEqual(body, (self.source / "assets/app-abcdefgh.js.gz").read_bytes())
        for path in ["/assets/custom.js", "/logo.svg", "/docs/legal/admin-compliance.en.md"]:
            with self.subTest(path=path):
                status, headers, _ = self.request(path)
                self.assertEqual(status, 200)
                self.assertEqual(headers["Cache-Control"], "no-cache")
        for path in ["/assets/missing-abcdefgh.js", "/missing.css", "/docs/brands/missing.png", "/.env"]:
            with self.subTest(path=path):
                status, headers, _ = self.request(path)
                self.assertEqual(status, 404)
                self.assertNotIn("immutable", headers.get("Cache-Control", ""))

    def test_backend_aliases_preserve_uri_and_post(self):
        for prefix in ["/api/v1", "/v1", "/v1beta", "/health", "/models", "/responses", "/messages", "/images", "/embeddings", "/chat/completions"]:
            for suffix in ["", "/example.js?limit=2"]:
                for method in ["GET", "POST"]:
                    with self.subTest(path=prefix+suffix, method=method):
                        status, headers, body = self.request(prefix + suffix, method)
                        self.assertEqual(status, 418)
                        actual = json.loads(body)
                        self.assertEqual(actual["uri"], prefix + suffix)
                        self.assertEqual(actual["method"], method)
                        self.assertEqual(actual["forwarded_proto"], "http")
                        self.assertNotIn("Cache-Control", headers)

    def test_websocket_upgrade_passes_through(self):
        status, headers, _ = self.request("/v1/ws?session=1", headers={"Upgrade": "websocket", "Connection": "Upgrade"})
        self.assertEqual(status, 101)
        self.assertEqual(headers["Upgrade"], "websocket")

    def test_old_hash_and_rollback_without_reload(self):
        (self.source / "index.html").write_text('<html>new<script src="/assets/app-ijklmnop.js"></script></html>')
        (self.source / "assets/app-ijklmnop.js").write_text("new chunk")
        (self.source / "assets/app-abcdefgh.js").unlink()
        with WEB.publication_lock(self.root):
            second = WEB.publish(self.root, self.source, "second-fixture-image")
        self.assertEqual(self.request("/assets/app-abcdefgh.js")[2], self.original)
        self.assertIn(b"new<script", self.request("/")[2])
        with WEB.publication_lock(self.root):
            WEB.rollback(self.root, self.first)
        self.assertNotIn(b"new<script", self.request("/")[2])
        self.assertEqual(self.request("/assets/app-ijklmnop.js")[2], b"new chunk")
        self.assertEqual(os.readlink(self.root / "previous"), f"releases/{second}")


if __name__ == "__main__":
    unittest.main()
