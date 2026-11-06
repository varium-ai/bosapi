#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

docker compose --env-file deploy/stack.env.example -f deploy/docker-compose.yml config --format json | python3 -c '
import json
import sys

config = json.load(sys.stdin)
services = config["services"]
assert config["name"] == "bosapi-stack"
assert set(services) == {"app", "mysql", "redis"}
assert "no-new-privileges:true" in services["app"]["security_opt"]
assert services["mysql"]["ports"] == [{"mode": "ingress", "target": 3306, "published": "13306", "protocol": "tcp", "host_ip": "127.0.0.1"}]
assert services["redis"].get("ports", []) == []
assert services["app"]["ports"] == [{"mode": "ingress", "target": 8081, "published": "18080", "protocol": "tcp", "host_ip": "127.0.0.1"}]
assert services["mysql"]["volumes"][0]["target"] == "/var/lib/mysql"
assert services["redis"]["volumes"][0]["target"] == "/data"
assert services["app"]["volumes"][0]["target"] == "/app/data"
assert services["app"]["read_only"] is True
assert any(mount.startswith("/tmp:") for mount in services["app"]["tmpfs"])
for service in services.values():
    assert service["logging"]["driver"] == "json-file"
    assert service["logging"]["options"] == {"max-size": "10m", "max-file": "10"}
print("docker compose security test passed")
'

BOS_MYSQL_DATA_MOUNT=/srv/bosapi/mysql \
BOS_REDIS_DATA_MOUNT=/srv/bosapi/redis \
BOS_GATEWAY_DATA_MOUNT=/srv/bosapi/gateway \
docker compose --env-file deploy/stack.env.example -f deploy/docker-compose.yml config --format json | python3 -c '
import json
import sys

services = json.load(sys.stdin)["services"]
for name, directory, target in (
    ("mysql", "mysql", "/var/lib/mysql"),
    ("redis", "redis", "/data"),
    ("app", "gateway", "/app/data"),
):
    mount = services[name]["volumes"][0]
    assert mount["type"] == "bind"
    assert mount["source"] == "/srv/bosapi/" + directory
    assert mount["target"] == target
print("docker compose bind mount test passed")
'

BOS_INSTALL_DOMAIN=api.example.com \
docker compose --env-file deploy/stack.env.example \
  -f deploy/docker-compose.yml -f deploy/compose.https.yml config --format json | python3 -c '
import json
import sys

config = json.load(sys.stdin)
services = config["services"]
assert set(services) == {"app", "mysql", "redis", "caddy"}
caddy = services["caddy"]
assert caddy["environment"]["BOS_INSTALL_DOMAIN"] == "api.example.com"
assert caddy["depends_on"]["app"]["condition"] == "service_healthy"
assert {p["target"] for p in caddy["ports"]} == {80, 443}
assert set(caddy["networks"]) == {"backend"}
mounts = {m["target"]: m for m in caddy["volumes"]}
assert mounts["/etc/caddy/Caddyfile"]["read_only"]
assert mounts["/data"]["source"] == "caddy_data"
assert mounts["/config"]["source"] == "caddy_config"
assert config["volumes"]["caddy_data"]["name"] == "bosapi-stack_caddy_data"
assert services["app"]["ports"][0]["host_ip"] == "127.0.0.1"
assert services["mysql"]["ports"][0]["host_ip"] == "127.0.0.1"
assert services["redis"].get("ports", []) == []
print("docker compose HTTPS overlay test passed")
'

# Explicit public HTTP survives either source or pinned-image deployment while
# the database and Redis keep their existing host exposure.
public_env="$(mktemp "${TMPDIR:-/tmp}/bosapi-public-env.XXXXXX")"
trap 'rm -f "$public_env"' EXIT
sed 's/^BOSAPI_PORT=.*/BOSAPI_PORT=19091/' deploy/stack.env.example > "$public_env"
printf 'BOSAPI_BIND_IP=0.0.0.0\n' >> "$public_env"
for mode in source image; do
  compose_files=(-f deploy/docker-compose.yml)
  if [[ "$mode" == image ]]; then
    compose_files+=(-f deploy/compose.image.yml)
  fi
  BOS_APP_IMAGE=registry.example/bosapi:test \
    docker compose --env-file "$public_env" "${compose_files[@]}" config --format json | python3 -c '
import json
import sys

services = json.load(sys.stdin)["services"]
assert services["app"]["ports"] == [{"mode": "ingress", "target": 8081, "published": "19091", "protocol": "tcp", "host_ip": "0.0.0.0"}]
assert services["mysql"]["ports"][0]["host_ip"] == "127.0.0.1"
assert services["redis"].get("ports", []) == []
print("docker compose public HTTP test passed")
'
done
