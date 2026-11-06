#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

# The local-image installer must tag the same image that --no-build resolves.
docker compose --env-file deploy/stack.env.example -f deploy/docker-compose.yml config --images | grep -Fxq bosapi-stack-app

docker compose --env-file deploy/stack.env.example -f deploy/docker-compose.yml config --format json | python3 -c '
import json
import sys

services = json.load(sys.stdin)["services"]
assert services["mysql"]["image"] == "swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/mysql:8.4"
assert services["redis"]["image"] == "docker.m.daocloud.io/library/redis:7.4-alpine"
app = services["app"]
assert app["build"]["args"]["BASE_IMAGE_REGISTRY"] == "docker.m.daocloud.io/library"
environment = app["environment"]
assert "@tcp(mysql:3306)/" in environment["BOS_DATABASE_DSN"]
assert environment["BOS_REDIS_URL"].endswith("@redis:6379/0")
assert "APP_ENV" not in environment
assert environment["BOS_USAGE_REPORTING_FORCE_DISABLED"] == "false"
assert "BOS_USAGE_REPORTING_ENDPOINT" not in environment
assert environment["BOS_USAGE_REPORTING_HTTP_TIMEOUT"] == "5s"
assert environment["BOS_ADMIN_EMAIL"] == "admin@bosapi.local"
assert environment["BOS_ADMIN_PASSWORD"] == "replace-with-a-random-password-at-least-12-characters"
assert app["depends_on"]["mysql"]["condition"] == "service_healthy"
assert app["depends_on"]["redis"]["condition"] == "service_healthy"
assert services["mysql"]["healthcheck"]["test"][0] == "CMD-SHELL"
assert services["redis"]["healthcheck"]["test"][0] == "CMD-SHELL"
assert services["app"]["healthcheck"]["test"][-1].endswith("/health/ready")
print("docker compose gateway environment test passed")
'

BOS_BASE_IMAGE_REGISTRY=docker.io/library \
docker compose --env-file deploy/stack.env.example -f deploy/docker-compose.yml config --format json | python3 -c '
import json
import sys

app = json.load(sys.stdin)["services"]["app"]
assert app["build"]["args"]["BASE_IMAGE_REGISTRY"] == "docker.io/library"
print("docker compose build registry override test passed")
'
