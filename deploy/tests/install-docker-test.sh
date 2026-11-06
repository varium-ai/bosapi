#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/bosapi-install-docker-test.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"
export INSTALLER_PATH="$repo_root/deploy/install-docker.sh"
export INSTALLER_LOG="$test_root/commands.log"
export INSTALLER_REPOSITORY="$test_root/docker.sources"

# All host mutations are mocked; the installer runs in a separate Bash process.
cat > "$test_root/runner.sh" <<'EOF'
#!/usr/bin/env bash
source() {
  [[ "$1" == /etc/os-release ]] || return 1
  ID="${MOCK_OS_ID:-ubuntu}"
  VERSION_ID="${MOCK_OS_VERSION:-26.04}"
}
builtin source "$INSTALLER_PATH" "$@"
EOF

cat > "$test_root/bin/mock-command" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
command_name="${0##*/}"
case "$command_name" in
  uname) printf '%s\n' "${MOCK_KERNEL:-Linux}" ;;
  id)
    if [[ "$1" == -u ]]; then
      printf '%s\n' "${MOCK_UID:-0}"
    else
      [[ "${MOCK_MISSING_USER:-0}" != 1 ]]
    fi
    ;;
  dpkg-query)
    if [[ "${MOCK_CONFLICT:-}" == "${@: -1}" ]]; then
      printf 'install ok installed'
    else
      exit 1
    fi
    ;;
  dpkg) printf 'amd64\n' ;;
  *)
    printf '%s %s\n' "$command_name" "$*" >> "$INSTALLER_LOG"
    [[ "${MOCK_FAIL:-}" != "$command_name" ]] || exit 1
    case "$command_name" in
      curl)
        while [[ $# -gt 0 ]]; do
          if [[ "$1" == -o ]]; then
            printf 'mock signing key\n' > "$2"
            break
          fi
          shift
        done
        ;;
      tee) cat > "$INSTALLER_REPOSITORY" ;;
      docker)
        [[ "${MOCK_FAIL:-}:$*" != docker-info:info ]] || exit 1
        printf 'mock Docker version\n'
        ;;
    esac
    ;;
esac
EOF
chmod +x "$test_root/bin/mock-command"
for command_name in uname id dpkg-query dpkg apt-get curl install tee systemctl docker usermod; do
  ln -s mock-command "$test_root/bin/$command_name"
done
export PATH="$test_root/bin:$PATH"
export SUDO_USER=ubuntu

run_installer() {
  : > "$INSTALLER_LOG"
  bash "$test_root/runner.sh" "$@" > "$test_root/output.log" 2>&1
}

assert_failure() {
  local expected_error="$1"
  shift
  if run_installer "$@"; then
    printf 'Installer must fail: %s\n' "$expected_error" >&2
    exit 1
  fi
  grep -Fq -- "$expected_error" "$test_root/output.log"
}

run_installer
grep -Fxq 'Suites: resolute' "$INSTALLER_REPOSITORY"
grep -Fxq 'Architectures: amd64' "$INSTALLER_REPOSITORY"
grep -Fxq 'URIs: https://download.docker.com/linux/ubuntu' "$INSTALLER_REPOSITORY"
grep -Fxq 'apt-get install -y ca-certificates curl openssl rsync git' "$INSTALLER_LOG"
grep -Fxq 'apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin' "$INSTALLER_LOG"
grep -Fxq 'systemctl enable --now docker.service containerd.service' "$INSTALLER_LOG"
grep -Fxq 'docker compose version' "$INSTALLER_LOG"
grep -Fxq 'docker buildx version' "$INSTALLER_LOG"
grep -Fxq 'docker info' "$INSTALLER_LOG"
grep -Fxq 'usermod -aG docker -- ubuntu' "$INSTALLER_LOG"
grep -q 'Reconnect SSH' "$test_root/output.log"

run_installer --user deployer
grep -Fxq 'usermod -aG docker -- deployer' "$INSTALLER_LOG"
SUDO_USER= run_installer
! grep -q '^usermod ' "$INSTALLER_LOG"
run_installer --user root
! grep -q '^usermod ' "$INSTALLER_LOG"

MOCK_KERNEL=Darwin run_installer --help
test ! -s "$INSTALLER_LOG"
assert_failure 'Unknown option' --unknown
assert_failure '--user requires a username' --user
MOCK_KERNEL=Darwin assert_failure 'Run this script on the Ubuntu server'
MOCK_OS_ID=debian assert_failure 'supports Ubuntu 26.04 only'
MOCK_OS_VERSION=24.04 assert_failure 'supports Ubuntu 26.04 only'
MOCK_UID=1000 assert_failure 'Root access is required'
MOCK_MISSING_USER=1 assert_failure 'Deployment user does not exist'
MOCK_CONFLICT=docker.io assert_failure 'Conflicting packages are installed: docker.io'
test ! -s "$INSTALLER_LOG"

for failure in apt-get curl systemctl docker-info; do
  MOCK_FAIL="$failure" assert_failure 'Installation failed at line'
  ! grep -q '^usermod ' "$INSTALLER_LOG"
  ! grep -q 'Docker environment is ready' "$test_root/output.log"
  if [[ "$failure" == curl ]]; then
    ! grep -q '^install \|^tee \|^systemctl ' "$INSTALLER_LOG"
  fi
done

printf 'Docker installer test passed\n'
