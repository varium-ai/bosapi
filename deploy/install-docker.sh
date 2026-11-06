#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: sudo bash deploy/install-docker.sh [--user USER]

Install Docker Engine, Compose, Buildx, and deployment tools on Ubuntu 26.04.
Enable Docker and containerd at boot and verify the installation.

  --user USER  Grant Docker access to this deployment user (default: SUDO_USER)
  -h, --help   Show this help

Docker group membership grants root-level privileges. Reconnect SSH afterward.
This installs the repository's current packages, including upgrades on reruns.
EOF
}

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

deploy_user="${SUDO_USER:-}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)
      [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail '--user requires a username.'
      deploy_user="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) fail "Unknown option: $1. Use --help for usage." ;;
  esac
done

[[ "$(uname -s)" == Linux ]] || fail 'Run this script on the Ubuntu server.'
source /etc/os-release
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == 26.04 ]] || fail 'This script supports Ubuntu 26.04 only.'
[[ "$(id -u)" == 0 ]] || fail 'Root access is required. Run: sudo bash deploy/install-docker.sh'
if [[ -n "$deploy_user" ]]; then
  id -- "$deploy_user" >/dev/null 2>&1 || fail "Deployment user does not exist: $deploy_user."
fi

# Never remove another container runtime or distribution-provided installation.
conflicting_packages=()
for package in docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc; do
  if [[ "$(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true)" == 'install ok installed' ]]; then
    conflicting_packages+=("$package")
  fi
done
if [[ ${#conflicting_packages[@]} -gt 0 ]]; then
  fail "Conflicting packages are installed: ${conflicting_packages[*]}. Review them before removing them and retrying. See https://docs.docker.com/engine/install/ubuntu/"
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
trap 'printf "Installation failed at line %s. Resolve the error above and rerun the script.\n" "$LINENO" >&2' ERR

printf 'Installing deployment tools...\n'
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl openssl rsync git

printf 'Configuring the official Docker repository...\n'
curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
  https://download.docker.com/linux/ubuntu/gpg -o "$work_dir/docker.asc"
[[ -s "$work_dir/docker.asc" ]] || fail 'The downloaded Docker signing key is empty.'
install -m 0755 -d /etc/apt/keyrings
install -m 0644 "$work_dir/docker.asc" /etc/apt/keyrings/docker.asc
tee /etc/apt/sources.list.d/docker.sources >/dev/null <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: resolute
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF

printf 'Installing Docker Engine, Compose, and Buildx...\n'
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker.service containerd.service

printf 'Verifying Docker...\n'
docker --version
docker compose version
docker buildx version
docker info >/dev/null

if [[ -n "$deploy_user" && "$deploy_user" != root ]]; then
  usermod -aG docker -- "$deploy_user"
  printf 'Docker access granted to %s (root-level privileges). Reconnect SSH as this user before deploying.\n' "$deploy_user"
fi

printf 'Docker environment is ready. From the project root, run:\n'
printf '  docker info\n  ./deploy/stack.sh up\n  ./deploy/stack.sh status\n  curl -fsS http://127.0.0.1:18080/health/ready\n'
