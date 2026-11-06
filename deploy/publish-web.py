#!/usr/bin/env python3
"""Publish image frontend files without making Nginx a stack dependency."""

import argparse
import contextlib
import ctypes
import ctypes.util
import datetime
import fcntl
import gzip
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit
import uuid
import zlib


HASHED_ASSET = re.compile(r"^assets/.+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+(?:\.(?:br|gz))?$")
RELEASE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$")
JS_REFERENCE = re.compile(
    r'''(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s*)["'](\.\.?/[^"']+)["']'''
    r'''|["'](/?assets/[^"'\s]+)["']'''
)
CSS_REFERENCE = re.compile(r'''url\(\s*["']?([^\s"')]+)''')


class ResourceParser(HTMLParser):
    """Collect entry resources while allowing normal SPA navigation links."""

    def __init__(self):
        super().__init__()
        self.references = []

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag in {"script", "img", "source"} and values.get("src"):
            self.references.append(values["src"])
        if tag == "link" and values.get("href"):
            self.references.append(values["href"])


def digest(path):
    """Hash whole files so immutable URLs cannot silently change contents."""
    with path.open("rb") as stream:
        return hashlib.sha256(stream.read()).hexdigest()


def encoding_matches_original(encoded_path, original_path):
    """Verify legacy sidecars without expanding beyond the expected file size."""
    original = original_path.read_bytes()
    if encoded_path.suffix == ".gz":
        try:
            with gzip.open(encoded_path, "rb") as stream:
                return stream.read(len(original) + 1) == original
        except (OSError, EOFError, zlib.error):
            return False
    library = ctypes.util.find_library("brotlidec")
    if not library:
        raise ValueError(f"libbrotlidec is required to verify untracked Brotli asset: {encoded_path.name}")
    try:
        decoder = ctypes.CDLL(library).BrotliDecoderDecompress
    except (OSError, AttributeError) as error:
        raise ValueError(f"cannot load libbrotlidec to verify: {encoded_path.name}") from error
    decoder.argtypes = [ctypes.c_size_t, ctypes.c_char_p,
                        ctypes.POINTER(ctypes.c_size_t), ctypes.c_void_p]
    decoder.restype = ctypes.c_int
    encoded = encoded_path.read_bytes()
    length = ctypes.c_size_t(len(original) + 1)
    output = ctypes.create_string_buffer(length.value)
    result = decoder(len(encoded), encoded, ctypes.byref(length), output)
    return result == 1 and output.raw[:length.value] == original


def check_reference(source, parent, reference):
    """Require local build references to exist inside the exported tree."""
    parsed = urlsplit(reference)
    if parsed.scheme or parsed.netloc or not parsed.path:
        return
    name = unquote(parsed.path)
    target = source / name.lstrip("/") if name.startswith("/") else parent / name
    target = target.resolve()
    if not target.is_relative_to(source) or not target.is_file():
        raise ValueError(f"missing or unsafe frontend reference: {reference}")


def validate_tree(source):
    """Reject incomplete builds, links, hidden files, and secret material."""
    if source.is_symlink() or not source.is_dir():
        raise ValueError("frontend source must be a real directory")
    source = source.resolve()
    files = {}
    for entry in source.rglob("*"):
        name = entry.relative_to(source).as_posix()
        if entry.is_symlink() or not (entry.is_file() or entry.is_dir()):
            raise ValueError(f"unsupported frontend entry: {name}")
        if any(part.startswith(".") for part in entry.relative_to(source).parts):
            raise ValueError(f"hidden frontend entry: {name}")
        if entry.suffix.lower() in {".pem", ".key", ".env"}:
            raise ValueError(f"private frontend entry: {name}")
        if entry.is_file():
            files[name] = digest(entry)
    if not files.get("index.html") or not (source / "index.html").stat().st_size:
        raise ValueError("frontend index.html is missing or empty")
    for name in files:
        if name.endswith((".br", ".gz")) and name.rsplit(".", 1)[0] not in files:
            raise ValueError(f"compressed frontend asset has no source file: {name}")
    parser = ResourceParser()
    parser.feed((source / "index.html").read_text(encoding="utf-8"))
    if not any(name.startswith("assets/") and name.endswith(".js") for name in files):
        raise ValueError("frontend JavaScript assets are missing")
    for reference in parser.references:
        check_reference(source, source, reference)
    for name in files:
        entry = source / name
        if entry.suffix == ".js":
            for match in JS_REFERENCE.finditer(entry.read_text(encoding="utf-8")):
                reference = match.group(1) or match.group(2)
                parent = source if match.group(2) else entry.parent
                check_reference(source, parent, reference)
        elif entry.suffix == ".css":
            for reference in CSS_REFERENCE.findall(entry.read_text(encoding="utf-8")):
                check_reference(source, entry.parent, reference)
    # Public documents and logos must be present in the image build too.
    public = Path(__file__).resolve().parent.parent / "frontend/public"
    if public.is_dir():
        for entry in public.rglob("*"):
            # Builds omit desktop metadata, including files left by older uploads.
            # This only changes the source inventory; exported hidden files above
            # still fail validation, as do links and private frontend material.
            if (not entry.is_symlink() and entry.is_file()
                    and (entry.name in {".DS_Store", "Thumbs.db"} or entry.name.startswith("._"))):
                continue
            if entry.is_file() and entry.relative_to(public).as_posix() not in files:
                raise ValueError(f"missing public frontend file: {entry.relative_to(public)}")
    return files


def validate_root_path(value):
    """Validate the destination before creating or publishing anything."""
    root = Path(value)
    repository = Path(__file__).resolve().parent.parent
    if not root.is_absolute() or root == Path("/"):
        raise ValueError("static root must be an absolute dedicated directory")
    if any(part.is_symlink() for part in [root, *root.parents]):
        raise ValueError("static root and its parents must not be symlinks")
    root = root.resolve()
    if root.is_relative_to(repository) or repository.is_relative_to(root):
        raise ValueError("static root must be outside the repository")
    return root


def prepare_root(value):
    """Create a missing static root for the deployment user, using sudo if needed."""
    root = validate_root_path(value)
    if not root.exists():
        try:
            previous_umask = os.umask(0o022)
            try:
                root.mkdir(mode=0o755, parents=True, exist_ok=True)
            finally:
                os.umask(previous_umask)
        except PermissionError as error:
            if os.geteuid() == 0 or not shutil.which("sudo"):
                raise ValueError(f"cannot create static root {root}; create it with administrator permissions "
                                 "and grant the deployment user write access") from error
            print(f"Creating static root {root} with sudo for the deployment user", flush=True)
            subprocess.run(["sudo", "install", "-d", "-o", str(os.getuid()),
                            "-g", str(os.getgid()), "-m", "0755", "--", str(root)], check=True)
    return validate_root(value)


def validate_root(value):
    """Keep releases outside rsync's source tree and reject linked roots."""
    root = validate_root_path(value)
    if not root.is_dir() or not os.access(root, os.W_OK):
        raise ValueError("prepare a writable static root before enabling publication")
    for name in ("releases", "assets", "manifests", ".publish.lock"):
        if (root / name).is_symlink():
            raise ValueError(f"static publication path must not be a symlink: {name}")
    return root


@contextlib.contextmanager
def publication_lock(root):
    """Serialize publishers and rollbacks without leaving a stale lock."""
    with (root / ".publish.lock").open("a") as stream:
        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield


def current_release(root):
    """Accept only release links created inside this publication root."""
    link = root / "current"
    if not link.is_symlink():
        if link.exists():
            raise ValueError("current must be a symlink")
        return None
    target = os.readlink(link)
    release = target.removeprefix("releases/")
    if target != f"releases/{release}" or not RELEASE_ID.fullmatch(release):
        raise ValueError("current points outside releases")
    return release


def replace_link(root, name, release):
    """Use one rename so readers always see a complete release."""
    temporary = root / f".{name}-{uuid.uuid4().hex}"
    try:
        temporary.symlink_to(f"releases/{release}")
        os.replace(temporary, root / name)
    finally:
        temporary.unlink(missing_ok=True)


def switch_release(root, release):
    """Keep the previous target available before atomically switching current."""
    previous = current_release(root)
    if previous and previous != release:
        replace_link(root, "previous", previous)
    replace_link(root, "current", release)


def ensure_directory(path):
    """Provide readable directories without granting the Nginx user writes."""
    if path.is_symlink():
        raise ValueError(f"publication directory must not be a symlink: {path}")
    path.mkdir(exist_ok=True)
    path.chmod(0o755)


def publish(root, source, image, activate=True):
    """Prepare and validate a full release before changing the entry link."""
    files = validate_tree(source)
    current_release(root)
    for name in ("releases", "assets", "manifests"):
        ensure_directory(root / name)
    # Encoders can change bytes without changing Vite's source-content hash.
    # Reuse recorded encodings, or verify unrecorded legacy sidecars against
    # a recorded original. Never rewrite an existing immutable URL.
    reused_encodings = {}
    retained_files = None
    # Check all collisions before adding any new shared assets.
    for name, checksum in files.items():
        if HASHED_ASSET.fullmatch(name):
            target = root / name
            if target.is_symlink():
                raise ValueError(f"immutable asset collision: {name}")
            if target.exists() and digest(target) != checksum:
                original = name.rsplit(".", 1)[0]
                original_path = root / original
                if (name.endswith((".br", ".gz"))
                        and not original_path.is_symlink() and original_path.is_file()
                        and digest(original_path) == files[original]):
                    if retained_files is None:
                        retained_files = [
                            json.loads(path.read_text(encoding="utf-8"))["files"]
                            for path in sorted((root / "manifests").glob("*.json"))
                            if not path.is_symlink()
                        ]
                    existing_checksum = digest(target)
                    if any(record.get(name) == existing_checksum
                           and record.get(original) == files[original]
                           for record in retained_files):
                        reused_encodings[name] = existing_checksum
                        continue
                    # Older host-side compression left sidecars outside the
                    # manifests. Adopt only verified additions; never mask a
                    # changed encoding that a retained manifest already tracks.
                    if (not any(name in record for record in retained_files)
                            and any(record.get(original) == files[original]
                                    for record in retained_files)
                            and encoding_matches_original(target, source / original)):
                        reused_encodings[name] = existing_checksum
                        continue
                raise ValueError(f"immutable asset collision: {name}")
    release = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    release += "-" + uuid.uuid4().hex[:12]
    staging = Path(tempfile.mkdtemp(prefix=".staging-", dir=root))
    try:
        shutil.copytree(source, staging, dirs_exist_ok=True)
        for entry in [staging, *staging.rglob("*")]:
            entry.chmod(0o755 if entry.is_dir() else 0o644)
        if validate_tree(staging) != files:
            raise ValueError("frontend source changed during publication")
        for name in reused_encodings:
            shutil.copyfile(root / name, staging / name)
        files.update(reused_encodings)
        if reused_encodings and validate_tree(staging) != files:
            raise ValueError("retained frontend encoding changed during publication")
        for name in files:
            if not HASHED_ASSET.fullmatch(name):
                continue
            target = root / name
            for parent in reversed(target.relative_to(root).parents):
                ensure_directory(root / parent)
            if not target.exists():
                temporary = target.with_name(f".{target.name}-{uuid.uuid4().hex}")
                try:
                    shutil.copyfile(staging / name, temporary)
                    temporary.chmod(0o644)
                    os.replace(temporary, target)
                finally:
                    temporary.unlink(missing_ok=True)
        os.rename(staging, root / "releases" / release)
        manifest = {"release": release, "image": image, "files": files}
        manifest_path = root / "manifests" / f"{release}.json"
        manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        manifest_path.chmod(0o644)
        if activate:
            switch_release(root, release)
        return release
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def rollback(root, release):
    """Verify the retained release and its shared assets before rollback."""
    if not RELEASE_ID.fullmatch(release):
        raise ValueError("invalid release identifier")
    manifest = json.loads((root / "manifests" / f"{release}.json").read_text())
    source = root / "releases" / release
    if source.is_symlink() or not source.is_dir() or any(entry.is_symlink() for entry in source.rglob("*")):
        raise ValueError("retained release must contain only real files and directories")
    # Public files are checked against the manifest, not a newer checkout.
    actual = {entry.relative_to(source).as_posix(): digest(entry)
              for entry in source.rglob("*") if entry.is_file() and not entry.is_symlink()}
    if source.is_symlink() or actual != manifest["files"]:
        raise ValueError("retained release does not match its manifest")
    for name, checksum in actual.items():
        if HASHED_ASSET.fullmatch(name):
            asset = root / name
            if asset.is_symlink() or not asset.is_file() or digest(asset) != checksum:
                raise ValueError(f"retained shared asset is missing or changed: {name}")
    switch_release(root, release)


def docker(*args):
    """Call Docker directly and bound failures without executing shell input."""
    try:
        result = subprocess.run(["docker", *args], check=True, capture_output=True,
                                text=True, timeout=120)
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or "").strip() or f"exit status {error.returncode}"
        raise OSError(f"docker {args[0]} failed: {detail}") from error
    return result.stdout.strip()


def read_only_frontend(metadata):
    """Allow snapshot export only when container writes cannot affect frontend files."""
    if metadata.get("read_only") is not True:
        return False
    frontend = PurePosixPath("/app/web")
    for mount in metadata.get("mounts") or []:
        destination = mount.get("Destination", "")
        if not isinstance(destination, str) or not destination.startswith("/"):
            raise ValueError("invalid container mount destination")
        path = PurePosixPath(destination)
        if path == frontend or path in frontend.parents or frontend in path.parents:
            return False
    return True


@contextlib.contextmanager
def image_frontend(container):
    """Export immutable frontend bytes even after Docker retags the image index."""
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", container):
        raise ValueError("invalid app container identifier")
    # Select only public metadata; never inspect runtime credentials into output.
    template = ('{"id":{{json .Id}},"image":{{json .Image}},'
                '"read_only":{{json .HostConfig.ReadonlyRootfs}},"mounts":{{json .Mounts}}}')
    metadata = json.loads(docker("inspect", "--format", template, container))
    image = metadata.get("image", "")
    if not isinstance(image, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", image):
        raise ValueError("container did not resolve to an immutable image ID")
    container_id = metadata.get("id", "")
    if not isinstance(container_id, str) or not re.fullmatch(r"[0-9a-f]{64}", container_id):
        raise ValueError("container did not resolve to an immutable container ID")
    with tempfile.TemporaryDirectory(prefix="bosapi-web-") as directory:
        source = Path(directory) / "web"
        if read_only_frontend(metadata):
            # Docker 29 can retain an old image index on a running container
            # after a rebuild removes its image-store reference. Pin the
            # container ID, not its reusable name or a mutable image tag.
            docker("cp", f"{container_id}:/app/web", str(source))
        else:
            # Writable roots and frontend mounts cannot prove image contents.
            exported = docker("create", "--pull=never", image)
            try:
                docker("cp", f"{exported}:/app/web", str(source))
            finally:
                docker("rm", "-v", exported)
        yield source, image


def main():
    """Expose explicit validation, publication, and rollback operations."""
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    check = commands.add_parser("check-root")
    check.add_argument("--root", required=True)
    prepare = commands.add_parser("prepare-root")
    prepare.add_argument("--root", required=True)
    publish_command = commands.add_parser("publish")
    publish_command.add_argument("--root", required=True)
    publish_command.add_argument("--container", required=True)
    publish_command.add_argument("--stage", action="store_true", help="Validate and retain a release without switching current")
    rollback_command = commands.add_parser("rollback")
    rollback_command.add_argument("--root", required=True)
    rollback_command.add_argument("--release", required=True)
    args = parser.parse_args()
    if sys.version_info < (3, 9):
        parser.exit(1, "Static publication requires Python 3.9 or newer\n")
    try:
        root = prepare_root(args.root) if args.command == "prepare-root" else validate_root(args.root)
        if args.command in {"check-root", "prepare-root"}:
            current_release(root)
            return
        with publication_lock(root):
            if args.command == "publish":
                with image_frontend(args.container) as (source, image):
                    release = publish(root, source, image, activate=not args.stage)
                print(release if args.stage else f"Published frontend {release} from {image} at {root}")
            else:
                rollback(root, args.release)
                print(f"Restored frontend {args.release} at {root}")
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Frontend publication failed: {error}\n")


if __name__ == "__main__":
    main()
