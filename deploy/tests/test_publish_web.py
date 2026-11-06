"""Exercise publication failure boundaries, retained chunks and image export."""

import contextlib
import gzip
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


REPOSITORY = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("publish_web", REPOSITORY / "deploy/publish-web.py")
WEB = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WEB)

# Real Brotli streams of import("./lazy-abcdefgh.js"); with different windows.
BROTLI_APP = bytes.fromhex("030e80696d706f727428222e2f6c617a792d61626364656667682e6a7322293b03")
BROTLI_APP_NEW = bytes.fromhex("0b0e80696d706f727428222e2f6c617a792d61626364656667682e6a7322293b03")
BROTLI_OTHER = bytes.fromhex("1b2100e08d934cf5fed42728296fda65e78c4d17628826f0a8158b89e193261d2201")


class RootPreparationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root = self.base / "host-web"

    def test_check_does_not_create_missing_directory(self):
        with self.assertRaisesRegex(ValueError, "prepare a writable static root"):
            WEB.validate_root(str(self.root))
        self.assertFalse(self.root.exists())

    def test_creates_readable_parents_even_with_restrictive_umask(self):
        root = self.root / "nested public"
        previous_umask = os.umask(0o077)
        try:
            self.assertEqual(WEB.prepare_root(str(root)), root)
            observed_umask = os.umask(0o077)
            self.assertEqual(observed_umask, 0o077)
        finally:
            os.umask(previous_umask)
        for directory in (self.root, root):
            self.assertEqual(directory.stat().st_mode & 0o777, 0o755)

    def test_repeated_preparation_preserves_existing_permissions_and_files(self):
        self.root.mkdir(mode=0o750)
        retained = self.root / "published.txt"
        retained.write_text("keep existing release")
        before = self.root.stat()
        with patch.object(WEB.subprocess, "run") as command:
            WEB.prepare_root(str(self.root))
            WEB.prepare_root(str(self.root))
        after = self.root.stat()
        self.assertEqual((before.st_mode, before.st_uid, before.st_gid),
                         (after.st_mode, after.st_uid, after.st_gid))
        self.assertEqual(retained.read_text(), "keep existing release")
        command.assert_not_called()

    def test_invalid_paths_are_rejected_before_creation_or_sudo(self):
        for value in ("relative", "/", str(REPOSITORY), str(REPOSITORY.parent),
                      str(REPOSITORY / "new-static-root")):
            with self.subTest(value=value), patch.object(Path, "mkdir") as mkdir:
                with patch.object(WEB.subprocess, "run") as command:
                    with self.assertRaises(ValueError):
                        WEB.prepare_root(value)
                mkdir.assert_not_called()
                command.assert_not_called()

    def test_rejects_symlinked_root_or_parent_before_creation(self):
        target = self.base / "target"
        target.mkdir()
        linked = self.base / "linked"
        linked.symlink_to(target, target_is_directory=True)
        for value in (linked, linked / "new-root"):
            with self.subTest(value=value), patch.object(WEB.subprocess, "run") as command:
                with self.assertRaisesRegex(ValueError, "must not be symlinks"):
                    WEB.prepare_root(str(value))
                command.assert_not_called()
        self.assertFalse((target / "new-root").exists())

    def test_existing_nonwritable_root_is_not_reowned(self):
        self.root.mkdir()
        with patch.object(WEB.os, "access", return_value=False):
            with patch.object(WEB.subprocess, "run") as command:
                with self.assertRaisesRegex(ValueError, "prepare a writable static root"):
                    WEB.prepare_root(str(self.root))
                command.assert_not_called()

    def test_file_or_linked_publication_directory_is_not_replaced(self):
        self.root.write_text("keep file")
        with self.assertRaises(ValueError):
            WEB.prepare_root(str(self.root))
        self.assertEqual(self.root.read_text(), "keep file")
        self.root.unlink()
        self.root.mkdir()
        (self.root / "assets").symlink_to(self.base, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "must not be a symlink"):
            WEB.prepare_root(str(self.root))

    def test_permission_failure_creates_root_with_sudo_for_deployment_user(self):
        real_mkdir = Path.mkdir

        def install(*args, **kwargs):
            real_mkdir(self.root, mode=0o755)

        with patch.object(Path, "mkdir", side_effect=PermissionError("parent is root-owned")):
            with patch.object(WEB.os, "geteuid", return_value=1000):
                with patch.object(WEB.shutil, "which", return_value="/usr/bin/sudo"):
                    with patch.object(WEB.subprocess, "run", side_effect=install) as command:
                        with contextlib.redirect_stdout(io.StringIO()):
                            self.assertEqual(WEB.prepare_root(str(self.root)), self.root)
        command.assert_called_once_with(
            ["sudo", "install", "-d", "-o", str(os.getuid()), "-g", str(os.getgid()),
             "-m", "0755", "--", str(self.root)], check=True)

    def test_unavailable_or_failed_sudo_does_not_report_success(self):
        with patch.object(Path, "mkdir", side_effect=PermissionError("denied")):
            with patch.object(WEB.os, "geteuid", return_value=1000):
                with patch.object(WEB.shutil, "which", return_value=None):
                    with self.assertRaisesRegex(ValueError, "administrator permissions"):
                        WEB.prepare_root(str(self.root))
                with patch.object(WEB.shutil, "which", return_value="/usr/bin/sudo"):
                    with patch.object(WEB.subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["sudo"])):
                        with contextlib.redirect_stdout(io.StringIO()):
                            with self.assertRaises(subprocess.CalledProcessError):
                                WEB.prepare_root(str(self.root))
        self.assertFalse(self.root.exists())

    def test_prepare_root_command_creates_directory(self):
        with patch.object(sys, "argv", ["publish-web", "prepare-root", "--root", str(self.root)]):
            WEB.main()
        self.assertTrue(self.root.is_dir())


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name).resolve()
        self.root = self.base / "host-web"
        self.root.mkdir()
        self.source = self.base / "dist"
        shutil.copytree(REPOSITORY / "frontend/public", self.source,
                        ignore=shutil.ignore_patterns(".DS_Store", "Thumbs.db", "._*"))
        self.write("index.html", '<script type="module" src="/assets/app-abcdefgh.js"></script>')
        self.write("assets/app-abcdefgh.js", 'import("./lazy-abcdefgh.js");')
        self.write("assets/lazy-abcdefgh.js", "console.log('old');")
        self.write_bytes("assets/app-abcdefgh.js.gz", gzip.compress(b'import("./lazy-abcdefgh.js");', mtime=0))
        self.write_bytes("assets/app-abcdefgh.js.br", b"fixture-brotli")
        self.write("assets/custom.js", "unversioned old")

    def write(self, name, content):
        path = self.source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def write_bytes(self, name, content):
        path = self.source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def publish(self):
        with WEB.publication_lock(self.root):
            return WEB.publish(self.root, self.source, "sha256:" + "a" * 64)

    def test_publish_and_rollback_keep_old_chunks_and_version_public_files(self):
        first = self.publish()
        self.write("index.html", '<script src="/assets/app-ijklmnop.js"></script>')
        self.write("assets/app-ijklmnop.js", 'import("./lazy-ijklmnop.js");')
        self.write("assets/lazy-ijklmnop.js", "new chunk")
        (self.source / "assets/app-abcdefgh.js").unlink()
        (self.source / "assets/app-abcdefgh.js.gz").unlink()
        (self.source / "assets/app-abcdefgh.js.br").unlink()
        (self.source / "assets/lazy-abcdefgh.js").unlink()
        self.write("assets/custom.js", "unversioned new")
        self.write("logo.svg", "new logo")
        second = self.publish()
        self.assertEqual(os.readlink(self.root / "current"), f"releases/{second}")
        self.assertEqual(os.readlink(self.root / "previous"), f"releases/{first}")
        self.assertTrue((self.root / "assets/lazy-abcdefgh.js").is_file())
        self.assertTrue((self.root / "assets/app-abcdefgh.js.gz").is_file())
        self.assertTrue((self.root / "assets/app-abcdefgh.js.br").is_file())
        self.assertFalse((self.root / "assets/custom.js").exists())
        self.assertEqual((self.root / "current/assets/custom.js").read_text(), "unversioned new")
        manifest = json.loads((self.root / "manifests" / f"{second}.json").read_text())
        self.assertEqual(manifest["image"], "sha256:" + "a" * 64)
        for entry in (self.root / "current").resolve().rglob("*"):
            self.assertEqual(entry.stat().st_mode & 0o777, 0o755 if entry.is_dir() else 0o644)
        with WEB.publication_lock(self.root):
            WEB.rollback(self.root, first)
        self.assertEqual((self.root / "current/assets/custom.js").read_text(), "unversioned old")
        self.assertEqual(os.readlink(self.root / "previous"), f"releases/{second}")

    def test_incomplete_build_does_not_switch_current(self):
        first = self.publish()
        for missing in ["index.html", "assets/lazy-abcdefgh.js", "docs/legal/admin-compliance.en.md", "logo.png"]:
            with self.subTest(missing=missing):
                content = (self.source / missing).read_bytes()
                (self.source / missing).unlink()
                with self.assertRaises(ValueError):
                    self.publish()
                self.assertEqual(os.readlink(self.root / "current"), f"releases/{first}")
                (self.source / missing).write_bytes(content)

    def test_stage_validates_release_without_switching_current(self):
        first = self.publish()
        self.write("assets/custom.js", "candidate version")
        with WEB.publication_lock(self.root):
            candidate = WEB.publish(self.root, self.source, "candidate-image", activate=False)
        self.assertEqual(WEB.current_release(self.root), first)
        self.assertEqual((self.root / "releases" / candidate / "assets/custom.js").read_text(), "candidate version")
        with WEB.publication_lock(self.root):
            WEB.rollback(self.root, candidate)
        self.assertEqual(WEB.current_release(self.root), candidate)

    def test_rejects_compressed_asset_without_source_file(self):
        self.write_bytes("assets/orphan-abcdefgh.js.br", b"orphan")
        with self.assertRaisesRegex(ValueError, "has no source file"):
            self.publish()

    def test_immutable_collision_rejects_entire_publication(self):
        first = self.publish()
        self.write("assets/lazy-abcdefgh.js", "different content at the same immutable URL")
        with self.assertRaisesRegex(ValueError, "collision"):
            self.publish()
        self.assertEqual(os.readlink(self.root / "current"), f"releases/{first}")
        self.assertEqual((self.root / "assets/lazy-abcdefgh.js").read_text(), "console.log('old');")

    def test_reuses_published_encodings_and_keeps_both_releases_rollbackable(self):
        first = self.publish()
        names = ["assets/app-abcdefgh.js.gz", "assets/app-abcdefgh.js.br"]
        original = {name: (self.root / name).read_bytes() for name in names}
        self.write_bytes(names[0], gzip.compress((self.source / "assets/app-abcdefgh.js").read_bytes(),
                                               compresslevel=1, mtime=1))
        self.write_bytes(names[1], b"different-brotli-encoder-output")
        incoming = {name: (self.source / name).read_bytes() for name in names}
        second = self.publish()
        manifest = json.loads((self.root / "manifests" / f"{second}.json").read_text())
        for name in names:
            self.assertNotEqual(incoming[name], original[name])
            self.assertEqual((self.source / name).read_bytes(), incoming[name])
            self.assertEqual((self.root / name).read_bytes(), original[name])
            self.assertEqual((self.root / "current" / name).read_bytes(), original[name])
            self.assertEqual(manifest["files"][name], WEB.digest(self.root / name))
        with WEB.publication_lock(self.root):
            WEB.rollback(self.root, first)
            WEB.rollback(self.root, second)

    def test_encoding_reuse_rejects_changed_or_missing_original(self):
        for change in ["changed", "missing"]:
            with self.subTest(change=change):
                first = self.publish()
                asset = self.root / "assets/app-abcdefgh.js"
                original = asset.read_bytes()
                if change == "changed":
                    asset.write_bytes(b"changed original")
                else:
                    asset.unlink()
                self.write_bytes("assets/app-abcdefgh.js.br", b"new encoding")
                self.write("assets/new-abcdefgh.js", "must not be published on failure")
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)
                self.assertFalse((self.root / "assets/new-abcdefgh.js").exists())
                asset.write_bytes(original)
                (self.source / "assets/app-abcdefgh.js.br").write_bytes(b"fixture-brotli")
                (self.source / "assets/new-abcdefgh.js").unlink()

    def test_encoding_reuse_requires_manifest_for_exact_original_and_encoding(self):
        first = self.publish()
        manifest_path = self.root / "manifests" / f"{first}.json"
        manifest = json.loads(manifest_path.read_text())
        self.write_bytes("assets/app-abcdefgh.js.br", b"new encoding")
        for name in ["assets/app-abcdefgh.js", "assets/app-abcdefgh.js.br"]:
            with self.subTest(name=name):
                changed = json.loads(json.dumps(manifest))
                changed["files"][name] = "0" * 64
                manifest_path.write_text(json.dumps(changed))
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)
        manifest_path.unlink()
        with self.assertRaisesRegex(ValueError, "collision"):
            self.publish()

    def prepare_legacy_encoding(self, suffix):
        name = "assets/app-abcdefgh.js" + suffix
        (self.source / name).unlink()
        first = self.publish()
        original = (self.source / "assets/app-abcdefgh.js").read_bytes()
        if suffix == ".br":
            existing, incoming = BROTLI_APP, BROTLI_APP_NEW
        else:
            existing = gzip.compress(original, compresslevel=9, mtime=0)
            incoming = gzip.compress(original, compresslevel=1, mtime=1)
        (self.root / name).write_bytes(existing)
        self.write_bytes(name, incoming)
        return first, name, existing

    def check_legacy_encoding_adoption(self, suffix):
        first, name, existing = self.prepare_legacy_encoding(suffix)
        first_manifest_path = self.root / "manifests" / f"{first}.json"
        first_manifest = first_manifest_path.read_bytes()
        self.assertNotIn(name, json.loads(first_manifest)["files"])
        self.assertFalse((self.root / "releases" / first / name).exists())
        second = self.publish()
        self.assertEqual(first_manifest_path.read_bytes(), first_manifest)
        self.assertEqual((self.root / name).read_bytes(), existing)
        self.assertEqual((self.root / "current" / name).read_bytes(), existing)
        manifest = json.loads((self.root / "manifests" / f"{second}.json").read_text())
        self.assertEqual(manifest["files"][name], WEB.digest(self.root / name))
        with WEB.publication_lock(self.root):
            WEB.rollback(self.root, first)
            WEB.rollback(self.root, second)
        # The new manifest makes subsequent publications independent of decoding.
        with patch.object(WEB, "encoding_matches_original", side_effect=AssertionError("already recorded")):
            self.publish()

    def test_adopts_untracked_gzip_and_preserves_rollback(self):
        self.check_legacy_encoding_adoption(".gz")

    @unittest.skipUnless(WEB.ctypes.util.find_library("brotlidec"), "libbrotlidec unavailable")
    def test_adopts_untracked_brotli_and_preserves_rollback(self):
        self.check_legacy_encoding_adoption(".br")

    def test_legacy_gzip_rejects_corrupt_truncated_and_different_content(self):
        first, name, existing = self.prepare_legacy_encoding(".gz")
        self.write("assets/new-abcdefgh.js", "must not be published on failure")
        for encoded in [b"invalid gzip", existing[:-1], gzip.compress(b"different original"),
                        gzip.compress(b"x" * 100_000),
                        bytes.fromhex("1f8b0800000000000003ff")]:
            with self.subTest(encoded=encoded[:12]):
                (self.root / name).write_bytes(encoded)
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)
                self.assertFalse((self.root / "assets/new-abcdefgh.js").exists())

    @unittest.skipUnless(WEB.ctypes.util.find_library("brotlidec"), "libbrotlidec unavailable")
    def test_legacy_brotli_rejects_corrupt_truncated_and_different_content(self):
        first, name, existing = self.prepare_legacy_encoding(".br")
        self.write("assets/new-abcdefgh.js", "must not be published on failure")
        for encoded in [b"invalid brotli", existing[:-1], BROTLI_OTHER]:
            with self.subTest(encoded=encoded[:12]):
                (self.root / name).write_bytes(encoded)
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)
                self.assertFalse((self.root / "assets/new-abcdefgh.js").exists())

    def test_legacy_encoding_still_requires_a_recorded_original(self):
        first, name, _ = self.prepare_legacy_encoding(".gz")
        manifest_path = self.root / "manifests" / f"{first}.json"
        manifest = json.loads(manifest_path.read_text())
        for change in ("different", "missing"):
            with self.subTest(change=change):
                if change == "different":
                    manifest["files"]["assets/app-abcdefgh.js"] = "0" * 64
                    manifest_path.write_text(json.dumps(manifest))
                else:
                    manifest_path.unlink()
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)

    def test_changed_recorded_encoding_cannot_be_adopted_even_if_valid(self):
        first = self.publish()
        name = "assets/app-abcdefgh.js.gz"
        original = (self.source / "assets/app-abcdefgh.js").read_bytes()
        (self.root / name).write_bytes(gzip.compress(original, mtime=42))
        self.write_bytes(name, gzip.compress(original, mtime=43))
        with self.assertRaisesRegex(ValueError, "collision"):
            self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_missing_brotli_library_fails_before_switching_current(self):
        first, _, _ = self.prepare_legacy_encoding(".br")
        with patch.object(WEB.ctypes.util, "find_library", return_value=None):
            with self.assertRaisesRegex(ValueError, "libbrotlidec is required"):
                self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_legacy_encoding_changed_after_verification_is_rejected(self):
        first, name, _ = self.prepare_legacy_encoding(".gz")
        copytree = WEB.shutil.copytree

        def change_verified_encoding(source, destination, *args, **kwargs):
            result = copytree(source, destination, *args, **kwargs)
            (self.root / name).write_bytes(b"changed after verification")
            return result

        with patch.object(WEB.shutil, "copytree", side_effect=change_verified_encoding):
            with self.assertRaisesRegex(ValueError, "retained frontend encoding changed"):
                self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_encoding_reuse_rejects_corrupted_shared_encoding(self):
        first = self.publish()
        (self.root / "assets/app-abcdefgh.js.br").write_bytes(b"corrupted published encoding")
        self.write_bytes("assets/app-abcdefgh.js.br", b"new encoding")
        with self.assertRaisesRegex(ValueError, "collision"):
            self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_encoding_reuse_does_not_skip_source_change_validation(self):
        first = self.publish()
        self.write_bytes("assets/app-abcdefgh.js.br", b"new encoding")
        copytree = WEB.shutil.copytree

        def copy_changed_source(source, destination, *args, **kwargs):
            result = copytree(source, destination, *args, **kwargs)
            if Path(source) == self.source:
                (Path(destination) / "assets/app-abcdefgh.js.br").write_bytes(b"changed during copy")
            return result

        with patch.object(WEB.shutil, "copytree", side_effect=copy_changed_source):
            with self.assertRaisesRegex(ValueError, "source changed"):
                self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_immutable_links_are_rejected_even_when_dangling(self):
        first = self.publish()
        asset = self.root / "assets/app-abcdefgh.js.br"
        original = asset.read_bytes()
        asset.unlink()
        for destination in [self.source / "assets/app-abcdefgh.js.br", self.base / "missing"]:
            with self.subTest(destination=destination):
                asset.symlink_to(destination)
                with self.assertRaisesRegex(ValueError, "collision"):
                    self.publish()
                self.assertEqual(WEB.current_release(self.root), first)
                asset.unlink()
        asset.write_bytes(original)

    def test_reject_links_hidden_files_secrets_and_traversal(self):
        for name in [".env", "docs/.DS_Store", "docs/._logo.svg", "token.key", "secret.pem"]:
            with self.subTest(name=name):
                self.write(name, "private")
                with self.assertRaises(ValueError):
                    self.publish()
                (self.source / name).unlink()
        (self.source / "linked").symlink_to(self.base)
        with self.assertRaises(ValueError):
            self.publish()
        (self.source / "linked").unlink()
        self.write("index.html", '<script src="/../outside.js"></script>')
        with self.assertRaisesRegex(ValueError, "unsafe"):
            self.publish()
        self.assertFalse((self.root / "current").exists())

    def test_public_inventory_omits_only_desktop_metadata(self):
        checkout = self.base / "checkout"
        public = checkout / "frontend/public"
        shutil.copytree(self.source, public)
        for name in [".DS_Store", "docs/.DS_Store", "docs/Thumbs.db", "docs/legal/._admin.md"]:
            (public / name).write_text("desktop metadata")
        with patch.object(WEB, "__file__", str(checkout / "deploy/publish-web.py")):
            first = self.publish()
            for name in ["docs/.credentials", "docs/new-guide.md", "docs/legal/admin-compliance.en.md"]:
                with self.subTest(name=name):
                    target = public / name
                    target.write_text("required public file")
                    previous = (self.source / name).read_bytes() if (self.source / name).exists() else None
                    (self.source / name).unlink(missing_ok=True)
                    with self.assertRaisesRegex(ValueError, "missing public frontend file"):
                        self.publish()
                    self.assertEqual(WEB.current_release(self.root), first)
                    if previous is None:
                        target.unlink()
                    else:
                        (self.source / name).write_bytes(previous)

    def test_hidden_metadata_in_artifact_does_not_switch_current(self):
        first = self.publish()
        self.write("docs/.DS_Store", "desktop metadata")
        with self.assertRaisesRegex(ValueError, "hidden frontend entry: docs/.DS_Store"):
            self.publish()
        self.assertEqual(WEB.current_release(self.root), first)

    def test_checks_css_and_vite_preload_dependencies(self):
        self.write("assets/app-abcdefgh.js", 'const deps=["assets/missing-abcdefgh.css"];')
        with self.assertRaisesRegex(ValueError, "reference"):
            self.publish()
        self.write("assets/missing-abcdefgh.css", 'body{background:url(./font-abcdefgh.woff2)}')
        with self.assertRaisesRegex(ValueError, "reference"):
            self.publish()
        self.write("assets/font-abcdefgh.woff2", "font")
        self.publish()

    def test_rollback_rejects_modified_or_missing_files(self):
        first = self.publish()
        for release in ["../outside", "", "/tmp/anything"]:
            with self.assertRaises(ValueError):
                WEB.rollback(self.root, release)
        asset = self.root / "assets/lazy-abcdefgh.js"
        asset.unlink()
        with self.assertRaisesRegex(ValueError, "shared asset"):
            WEB.rollback(self.root, first)
        (self.root / "releases" / first / "logo.svg").write_text("changed")
        with self.assertRaisesRegex(ValueError, "manifest"):
            WEB.rollback(self.root, first)

    def test_switch_failure_leaves_old_release_usable(self):
        first = self.publish()
        original = os.replace

        def fail_current(source, destination):
            if Path(destination).name == "current":
                raise OSError("injected switch failure")
            return original(source, destination)

        with patch.object(WEB.os, "replace", side_effect=fail_current):
            with self.assertRaises(OSError):
                self.publish()
        self.assertEqual(os.readlink(self.root / "current"), f"releases/{first}")
        self.assertFalse(list(self.root.glob(".staging-*")))
        self.assertFalse(list(self.root.glob(".current-*")))

    def test_root_boundaries_and_lock(self):
        self.assertEqual(WEB.validate_root(str(self.root)), self.root)
        for root in ["relative", "/", str(REPOSITORY), str(REPOSITORY / "frontend")]:
            with self.subTest(root=root), self.assertRaises(ValueError):
                WEB.validate_root(root)
        linked = self.base / "linked-root"
        linked.symlink_to(self.root)
        with self.assertRaises(ValueError):
            WEB.validate_root(str(linked))
        with WEB.publication_lock(self.root):
            with self.assertRaises(BlockingIOError), WEB.publication_lock(self.root):
                pass

    def test_export_uses_image_and_cleans_container_on_copy_failure(self):
        image = "sha256:" + "a" * 64
        metadata = json.dumps({"id": "b" * 64, "image": image, "read_only": False, "mounts": []})
        with patch.object(WEB, "docker", side_effect=[metadata, "temporary-container", "", ""]) as mocked:
            with WEB.image_frontend("app-1") as (_, actual_image):
                self.assertEqual(actual_image, image)
            self.assertEqual(mocked.call_args_list[1].args, ("create", "--pull=never", image))
            self.assertEqual(mocked.call_args_list[-1].args, ("rm", "-v", "temporary-container"))
        with patch.object(WEB, "docker", side_effect=[metadata, "temporary-container", OSError("copy failed"), ""]) as mocked:
            with self.assertRaises(OSError), WEB.image_frontend("app-1"):
                pass
            self.assertEqual(mocked.call_args_list[-1].args, ("rm", "-v", "temporary-container"))
        with self.assertRaises(ValueError), WEB.image_frontend("--privileged"):
            pass

    def test_read_only_export_does_not_require_the_old_image_index(self):
        image = "sha256:" + "a" * 64
        container = "b" * 64
        metadata = json.dumps({"id": container, "image": image, "read_only": True,
                               "mounts": [{"Destination": "/app/data"}, {"Destination": "/tmp"}]})
        with patch.object(WEB, "docker", side_effect=[metadata, ""]) as mocked:
            with WEB.image_frontend("app-1") as (_, actual_image):
                self.assertEqual(actual_image, image)
            self.assertEqual(mocked.call_count, 2)
            self.assertEqual(mocked.call_args_list[1].args[0:2], ("cp", f"{container}:/app/web"))
            self.assertNotIn("latest", str(mocked.call_args_list))
            self.assertEqual([call.args[0] for call in mocked.call_args_list], ["inspect", "cp"])

    def test_read_only_export_copy_failure_keeps_current(self):
        first = self.publish()
        metadata = json.dumps({"id": "b" * 64, "image": "sha256:" + "a" * 64,
                               "read_only": True, "mounts": []})
        with patch.object(WEB, "docker", side_effect=[metadata, OSError("copy failed")]) as mocked:
            with self.assertRaises(OSError), WEB.image_frontend("app-1"):
                pass
            self.assertEqual(mocked.call_count, 2)
        self.assertEqual(WEB.current_release(self.root), first)

    def test_frontend_mount_boundaries_require_image_export(self):
        for destination in ["/", "/app", "/app/web", "/app/web/assets"]:
            with self.subTest(destination=destination):
                metadata = {"read_only": True, "mounts": [{"Destination": destination}]}
                self.assertFalse(WEB.read_only_frontend(metadata))
        for destination in ["/app/data", "/app/web-old", "/application", "/tmp"]:
            with self.subTest(destination=destination):
                metadata = {"read_only": True, "mounts": [{"Destination": destination}]}
                self.assertTrue(WEB.read_only_frontend(metadata))
        self.assertTrue(WEB.read_only_frontend({"read_only": True, "mounts": None}))
        with self.assertRaises(ValueError):
            WEB.read_only_frontend({"read_only": True, "mounts": [{"Destination": "relative"}]})
        image = "sha256:" + "a" * 64
        metadata = json.dumps({"id": "b" * 64, "image": image, "read_only": True,
                               "mounts": [{"Destination": "/app/web"}]})
        with patch.object(WEB, "docker", side_effect=[metadata, "temporary", "", ""]) as mocked:
            with WEB.image_frontend("app-1"):
                pass
            self.assertEqual(mocked.call_args_list[1].args, ("create", "--pull=never", image))

    def test_export_rejects_invalid_image_and_container_ids(self):
        for field, value in [("image", "latest"), ("id", "app-1")]:
            with self.subTest(field=field):
                metadata = {"id": "b" * 64, "image": "sha256:" + "a" * 64,
                            "read_only": True, "mounts": []}
                metadata[field] = value
                with patch.object(WEB, "docker", return_value=json.dumps(metadata)) as mocked:
                    with self.assertRaises(ValueError), WEB.image_frontend("app-1"):
                        pass
                    self.assertEqual(mocked.call_count, 1)

    def test_docker_failure_includes_daemon_error(self):
        error = subprocess.CalledProcessError(1, ["docker", "create"], stderr="No such image: sha256:missing\n")
        with patch.object(WEB.subprocess, "run", side_effect=error):
            with self.assertRaisesRegex(OSError, "docker create failed: No such image") as failure:
                WEB.docker("create", "--pull=never", "sha256:missing")
            self.assertIs(failure.exception.__cause__, error)

    def test_cli_check_publish_rollback_and_invalid_root(self):
        @contextlib.contextmanager
        def export(_):
            yield self.source, "sha256:" + "a" * 64

        with patch.object(sys, "argv", ["publish-web", "check-root", "--root", str(self.root)]):
            WEB.main()
        output = io.StringIO()
        with patch.object(sys, "argv", ["publish-web", "publish", "--root", str(self.root), "--container", "app"]):
            with patch.object(WEB, "image_frontend", export), contextlib.redirect_stdout(output):
                WEB.main()
        self.assertIn("Published frontend", output.getvalue())
        release = WEB.current_release(self.root)
        with patch.object(sys, "argv", ["publish-web", "rollback", "--root", str(self.root), "--release", release]):
            with contextlib.redirect_stdout(io.StringIO()):
                WEB.main()
        with patch.object(sys, "argv", ["publish-web", "check-root", "--root", "relative"]):
            with self.assertRaises(SystemExit) as failure, contextlib.redirect_stderr(io.StringIO()):
                WEB.main()
            self.assertEqual(failure.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
