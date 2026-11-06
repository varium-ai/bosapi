#!/usr/bin/env bash
set -euo pipefail

# Exercise mode selection and pin persistence without a Docker daemon, a host
# web directory, or a deployment target. Each case gets its own private stack.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/bosapi-deployment-modes.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

python3 - "$repo_root" "$test_root" <<'PY'
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT, TEMP = map(Path, sys.argv[1:])
OLD_PIN = "BOS_APP_IMAGE=registry.example/bosapi:previous\n"
TARGET_REF = "registry.example/bosapi:main"
TARGET_ID = "sha256:" + "b" * 64
SOURCE_ID = "sha256:" + "c" * 64

FAKE_DOCKER = r'''import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
base = Path(os.environ["BOS_MODE_CASE"])
failure = os.environ.get("BOS_MODE_FAILURE", "")
state_path = base / "docker-state.json"
state = json.loads(state_path.read_text())
pin_path = base / "repo/deploy/image.env"
event = {"args": args, "pin": pin_path.read_text() if pin_path.exists() else None}
command = ""
if args and args[0] == "compose":
    values, env_files, compose_files = {}, [], []
    index = 1
    while index < len(args):
        value = args[index]
        if value in ("--env-file", "-f"):
            path = Path(args[index + 1])
            if value == "-f":
                compose_files.append(str(path))
            else:
                env_files.append(str(path))
                for line in path.read_text().splitlines():
                    if line and not line.startswith("#") and "=" in line:
                        name, content = line.split("=", 1)
                        values[name] = content.strip("\"'")
            index += 2
        elif value in ("version", "config", "up", "ps", "logs", "down"):
            command = value
            break
        else:
            index += 1
    has_image_overlay = any(Path(path).name == "compose.image.yml" for path in compose_files)
    event.update(command=command, env_files=env_files, compose_files=compose_files,
                 image=(os.environ.get("BOS_APP_IMAGE", values.get("BOS_APP_IMAGE"))
                        if has_image_overlay else None))
with (base / "docker-events.jsonl").open("a") as stream:
    stream.write(json.dumps(event) + "\n")

if args == ["info"] or command in ("version", "config"):
    pass
elif args and args[0] == "pull":
    if failure == "pull":
        sys.exit(17)
elif args[:2] == ["image", "inspect"]:
    template = args[args.index("--format") + 1] if "--format" in args else ""
    if template == "{{.Id}}":
        if failure == "image-inspect":
            sys.exit(18)
        print("sha256:" + "b" * 64)
    elif failure == "version":
        sys.exit(19)
    else:
        print("test-version test-revision")
elif args and args[0] == "inspect":
    if failure == "version":
        sys.exit(19)
    print(state["image_id"])
elif command == "up":
    image = event["image"]
    state["image_id"] = image if image else "sha256:" + "c" * 64
    state_path.write_text(json.dumps(state))
    if failure == "start":
        sys.exit(20)
elif command == "ps":
    if "-q" in args:
        print("app-container-id")
else:
    raise SystemExit("Unexpected fake Docker invocation: " + repr(args))
'''

FAKE_PUBLISHER = r'''import json
import os
from pathlib import Path
import sys

base = Path(os.environ["BOS_MODE_CASE"])
pin = base / "repo/deploy/image.env"
with (base / "publisher-events.jsonl").open("a") as stream:
    stream.write(json.dumps({"args": sys.argv[1:],
                             "pin": pin.read_text() if pin.exists() else None}) + "\n")
if "publish" in sys.argv and os.environ.get("BOS_MODE_FAILURE") == "publish":
    raise SystemExit(21)
'''

ENVIRONMENT = """BOS_WEB_DELIVERY=go
MYSQL_DATABASE=bosapi
MYSQL_USER=bosapi
MYSQL_PASSWORD=preserve-mysql-password
MYSQL_ROOT_PASSWORD=preserve-root-password
REDIS_PASSWORD=preserve-redis-password
BOS_JWT_SIGNING_KEY=preserve-signing-key
BOS_ENCRYPTION_KEY=preserve-encryption-key
BOS_ADMIN_EMAIL=admin@example.test
BOS_ADMIN_PASSWORD=preserve-admin-password
"""


class Case:
    def __init__(self, name, pin=OLD_PIN):
        self.name = name
        self.base = TEMP / name
        self.deploy = self.base / "repo/deploy"
        self.deploy.mkdir(parents=True)
        self.bin = self.base / "bin"
        self.bin.mkdir()
        for relative in ("stack.sh", "docker-compose.yml", "compose.image.yml", "compose.https.yml",
                         "monitor/compose.monitor.yml"):
            destination = self.deploy / relative
            destination.parent.mkdir(exist_ok=True)
            shutil.copy(ROOT / "deploy" / relative, destination)
        (self.deploy / "publish-web.py").write_text(FAKE_PUBLISHER)
        self.env_file = self.deploy / "stack.env"
        self.env_file.write_text(ENVIRONMENT)
        self.env_file.chmod(0o600)
        self.pin = self.deploy / "image.env"
        if pin is not None:
            self.pin.write_text(pin)
            self.pin.chmod(0o600)
        (self.base / "docker-state.json").write_text(json.dumps({"image_id": "sha256:" + "a" * 64}))
        fake_docker = self.bin / "docker"
        fake_docker.write_text("#!" + sys.executable + "\n" + FAKE_DOCKER)
        fake_docker.chmod(0o755)
        self.environment = dict(os.environ)
        for key in ("BOS_APP_IMAGE", "BOS_WEB_DELIVERY", "BOS_WEB_ROOT", "COMPOSE_FILE", "COMPOSE_PROJECT_NAME"):
            self.environment.pop(key, None)
        self.environment.update(PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                                BOS_MODE_CASE=str(self.base))

    def run(self, *args, failure="", ambient_image=None, static=False):
        environment = dict(self.environment, BOS_MODE_FAILURE=failure)
        initial_files = {path for path in self.deploy.rglob("*") if path.is_file()}
        if ambient_image is not None:
            environment["BOS_APP_IMAGE"] = ambient_image
        if static:
            environment.update(BOS_WEB_DELIVERY="nginx", BOS_WEB_ROOT=str(self.base / "fake-web-root"))
        self.result = subprocess.run(["bash", str(self.deploy / "stack.sh"), *args],
                                     env=environment, text=True, stdout=subprocess.PIPE,
                                     stderr=subprocess.STDOUT)
        self.events = self.read_events("docker-events.jsonl")
        self.starts = [event for event in self.events if event.get("command") == "up"]
        self.pulls = [event for event in self.events if event["args"][:1] == ["pull"]]
        self.publishers = self.read_events("publisher-events.jsonl")
        self.check(self.env_file.read_text() == ENVIRONMENT, "deployment changed stack credentials")
        extra_files = {path for path in self.deploy.rglob("*") if path.is_file()} - initial_files - {self.pin}
        self.check(not extra_files, "temporary deployment files were left behind: " + repr(extra_files))
        return self

    def read_events(self, filename):
        path = self.base / filename
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def check(self, condition, message):
        if not condition:
            output = getattr(self, "result", None)
            raise AssertionError(self.name + ": " + message +
                                 ("\n" + output.stdout if output else ""))

    def success(self):
        self.check(self.result.returncode == 0, "expected successful deployment")

    def failed(self):
        self.check(self.result.returncode != 0, "expected failure")

    def pin_is(self, value):
        actual = self.pin.read_text() if self.pin.exists() else None
        self.check(actual == value, "unexpected persisted image pin: " + repr(actual))

    def single_start(self):
        self.check(len(self.starts) == 1, "expected exactly one activation attempt")
        return self.starts[0]

    def source_start(self):
        start = self.single_start()
        self.check("--build" in start["args"], "source deployment must build")
        self.check(not self.pulls, "source deployment must not pull an application image")
        self.check(all(not any(Path(path).name == "compose.image.yml"
                               for path in event.get("compose_files", []))
                       for event in self.events), "source mode loaded the pinned-image overlay")

    def image_start(self, image, *, local_only=False):
        start = self.single_start()
        args = start["args"]
        self.check(start["image"] == image, "Compose selected the wrong application image")
        self.check("--no-build" in args and "--build" not in args,
                   "image deployment must never build")
        global_pull = args[args.index("--pull") + 1] if "--pull" in args else None
        if local_only:
            self.check(global_pull == "never", "existing-image activation must stay local")
        else:
            self.check(global_pull != "never",
                       "explicit image deployment must permit pulling missing dependency images")
        self.check("--wait" in args, "image activation must wait for health")
        self.check(any(Path(path).name == "compose.image.yml" for path in start["compose_files"]),
                   "image deployment omitted its Compose overlay")


count = 0


def passed():
    global count
    count += 1


# Reject malformed requests before checking Docker or touching private state.
for number, args in enumerate((
        ("up", "--image"),
        ("up", "--image", ""),
        ("up", "--source", "--image", TARGET_REF),
        ("up", "--image", TARGET_REF, "--source"),
        ("up", "--source", "--source"),
        ("up", "--image", TARGET_REF, "unexpected"),
        ("up", "--image", "--quiet"),
        ("up", "--image", TARGET_REF + "\nBOS_APP_IMAGE=other:injected"),
        ("up", "--unknown"),
        ("up", "unexpected"))):
    case = Case("invalid-" + str(number)).run(*args)
    case.failed()
    case.pin_is(OLD_PIN)
    case.check(not case.events, "invalid arguments reached Docker")
    passed()

case = Case("source-over-old-pin").run("up", "--source", ambient_image="ambient:wrong")
case.success()
case.source_start()
case.pin_is(None)
case.check(case.starts[0]["pin"] == OLD_PIN, "source mode removed the old pin before activation")
passed()

case = Case("source-ignores-invalid-pin", pin="not a valid image env file\n").run("up", "--source")
case.success()
case.source_start()
case.pin_is(None)
passed()

case = Case("source-start-failure").run("up", "--source", failure="start")
case.failed()
case.source_start()
case.pin_is(OLD_PIN)
passed()

case = Case("source-publication-failure").run("up", "--source", failure="publish", static=True)
case.failed()
case.source_start()
case.pin_is(None)
case.check(any("publish" in event["args"] for event in case.publishers), "publisher was not exercised")
case.check(all(event["pin"] is None for event in case.publishers if "publish" in event["args"]),
           "successful backend source switch was not recorded before publication")
passed()

for initial_pin in (None, OLD_PIN):
    suffix = "new" if initial_pin is None else "existing"
    case = Case("image-success-" + suffix, pin=initial_pin).run(
        "up", "--image", TARGET_REF, ambient_image="ambient:wrong")
    case.success()
    case.image_start(TARGET_ID)
    case.pin_is("BOS_APP_IMAGE=" + TARGET_ID + "\n")
    case.check(len(case.pulls) == 1 and TARGET_REF in case.pulls[0]["args"],
               "explicit image deployment must pull the requested reference")
    case.check(case.starts[0]["pin"] == initial_pin, "new pin was persisted before backend health")
    case.check(case.pin.stat().st_mode & 0o777 == 0o600, "new pin is not private")
    passed()

    for failure in ("pull", "image-inspect", "start"):
        case = Case("image-" + failure + "-" + suffix, pin=initial_pin).run(
            "up", "--image", TARGET_REF, failure=failure)
        case.failed()
        case.pin_is(initial_pin)
        if failure == "start":
            case.image_start(TARGET_ID)
        else:
            case.check(not case.starts, "failed image preparation still activated an app")
        passed()

case = Case("image-publication-failure").run("up", "--image", TARGET_REF, failure="publish", static=True)
case.failed()
case.image_start(TARGET_ID)
case.pin_is("BOS_APP_IMAGE=" + TARGET_ID + "\n")
case.check(any("publish" in event["args"] for event in case.publishers), "publisher was not exercised")
case.check(all(event["pin"] == "BOS_APP_IMAGE=" + TARGET_ID + "\n"
               for event in case.publishers if "publish" in event["args"]),
           "healthy backend image switch was not recorded before publication")
passed()

case = Case("auto-source", pin=None).run("up")
case.success()
case.source_start()
case.pin_is(None)
passed()

case = Case("auto-image").run("up")
case.success()
case.image_start("registry.example/bosapi:previous", local_only=True)
case.pin_is(OLD_PIN)
case.check(not case.pulls, "automatic image mode unexpectedly refreshed a pin")
passed()

# Optional deployment overlays must survive either explicit mode.
for mode in ("source", "image"):
    case = Case("overlays-" + mode)
    for name, contents in (("https.enabled", ""), ("monitor.enabled", "")):
        (case.deploy / name).write_text(contents)
    case.run("up", "--source") if mode == "source" else case.run("up", "--image", TARGET_REF)
    case.success()
    names = {Path(path).name for path in case.single_start()["compose_files"]}
    case.check({"compose.https.yml", "compose.monitor.yml"}.issubset(names),
               "mode selection dropped an enabled overlay")
    case.check("caddy" in case.starts[0]["args"], "HTTPS service was omitted")
    case.source_start() if mode == "source" else case.image_start(TARGET_ID)
    passed()

case = Case("online-update-command-excluded").run("image-start")
case.failed()
case.pin_is(OLD_PIN)
case.check(not case.pulls and not case.starts, "removed online-update command changed the deployment")
passed()

case = Case("best-effort-version").run("up", "--image", TARGET_REF, failure="version")
case.success()
case.image_start(TARGET_ID)
case.pin_is("BOS_APP_IMAGE=" + TARGET_ID + "\n")
passed()

# Compose config is a local merge only: no Docker daemon or image operations.
# The app remains pinned while a new host can obtain its missing dependencies.
real_docker = shutil.which("docker")
if real_docker:
    case = Case("real-compose-pull-policies")
    environment = dict(os.environ, BOS_APP_IMAGE=TARGET_ID, BOS_INSTALL_DOMAIN="example.test")
    for key in ("COMPOSE_FILE", "COMPOSE_PROJECT_NAME"):
        environment.pop(key, None)
    result = subprocess.run([
        real_docker, "compose", "--env-file", str(case.env_file),
        "-f", str(ROOT / "deploy/docker-compose.yml"),
        "-f", str(ROOT / "deploy/compose.image.yml"),
        "-f", str(ROOT / "deploy/compose.https.yml"), "config", "--format", "json",
    ], env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    case.check(result.returncode == 0, "real Compose config failed:\n" + result.stderr)
    services = json.loads(result.stdout)["services"]
    case.check(services["app"]["image"] == TARGET_ID, "Compose did not preserve the immutable app image")
    case.check(services["app"].get("pull_policy") == "never", "Compose permits repulling the pinned app")
    for name in ("mysql", "redis", "caddy"):
        case.check(services[name].get("pull_policy") != "never",
                   name + " cannot pull its image on a new installation")
    passed()
else:
    print("SKIP: real Compose pull-policy merge check (Docker CLI unavailable)")

print("PASS: " + str(count) + " deployment mode cases (isolated fake Docker and publisher)")
PY
