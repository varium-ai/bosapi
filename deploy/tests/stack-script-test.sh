#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/bosapi-stack-test.XXXXXX")"
test_root="$(cd "$test_root" && pwd -P)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/repo/deploy" "$test_root/repo/backend/migrations" "$test_root/bin"
cp "$repo_root/deploy/stack.sh" "$test_root/repo/deploy/stack.sh"
cp "$repo_root/deploy/docker-compose.yml" "$test_root/repo/deploy/docker-compose.yml"
cp "$repo_root/deploy/publish-web.py" "$test_root/repo/deploy/publish-web.py"
git -C "$test_root/repo" init -q
git -C "$test_root/repo" symbolic-ref HEAD refs/heads/dev

cat > "$test_root/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "${DOCKER_FAILURE_MODE:-}:$*" in
  compose:'compose version'|daemon:info) exit 1 ;;
  config:*' config --quiet'|missing-image:*' up -d --no-build --wait app') exit 1 ;;
esac
case "$*" in
  *' ps -q app') printf 'app-container-id\n' ;;
esac
EOF
chmod +x "$test_root/bin/docker"
export REAL_RSYNC="$(command -v rsync)"
export PATH="$test_root/bin:$PATH"
export DOCKER_LOG="$test_root/docker.log"
# Existing baseline cases explicitly exercise the compatible Go-only mode.
export BOS_WEB_DELIVERY=go

env_file="$test_root/repo/deploy/stack.env"
for command in status logs down; do
  if "$test_root/repo/deploy/stack.sh" "$command" > "$test_root/missing-env.log" 2>&1; then
    printf '%s must require an existing environment file\n' "$command" >&2
    exit 1
  fi
  test ! -e "$env_file"
done
test ! -s "$DOCKER_LOG"

# Starting existing images must not initialize a missing private env file.
if "$test_root/repo/deploy/stack.sh" start > "$test_root/start-missing-env.log" 2>&1; then
  printf 'start must require an existing environment file\n' >&2
  exit 1
fi
grep -q 'restore the existing stack environment file' "$test_root/start-missing-env.log"
test ! -e "$env_file"
printf 'compose version\ninfo\n' > "$test_root/expected-preflight.log"
cmp "$test_root/expected-preflight.log" "$DOCKER_LOG"
rm "$DOCKER_LOG"

# Docker failures must precede creating or upgrading the private env file.
mkdir -p "$test_root/no-docker-bin"
ln -s "$(command -v bash)" "$test_root/no-docker-bin/bash"
ln -s "$(command -v dirname)" "$test_root/no-docker-bin/dirname"
test_path="$PATH"
for failure in missing compose daemon; do
  case "$failure" in
    missing)
      failure_path="$test_root/no-docker-bin"
      expected_error='Docker is not installed or not on PATH'
      ;;
    compose)
      failure_path="$test_path"
      expected_error='Docker Compose v2 is unavailable'
      ;;
    daemon)
      failure_path="$test_path"
      expected_error='Cannot access the Docker daemon'
      ;;
  esac
  for command in up start; do
    for env_state in missing existing; do
      if [[ "$env_state" == existing ]]; then
        printf 'MYSQL_PASSWORD=preserve-me\n' > "$env_file"
        chmod 644 "$env_file"
        cp "$env_file" "$test_root/preflight.env"
      fi
      if PATH="$failure_path" DOCKER_FAILURE_MODE="$failure" "$test_root/repo/deploy/stack.sh" "$command" > "$test_root/preflight.log" 2>&1; then
        printf '%s must reject Docker failure: %s\n' "$command" "$failure" >&2
        exit 1
      fi
      grep -q "$expected_error" "$test_root/preflight.log"
      if [[ "$env_state" == existing ]]; then
        cmp "$test_root/preflight.env" "$env_file"
        test "$(find "$env_file" -perm 644 -print)" = "$env_file"
        rm "$env_file"
      else
        test ! -e "$env_file"
      fi
    done
  done
done
! grep -q ' config --quiet$\| up ' "$DOCKER_LOG"
rm "$DOCKER_LOG"

DOCKER_FAILURE_MODE=compose "$test_root/repo/deploy/stack.sh" init > "$test_root/init.log"
test -f "$env_file"
test "$(find "$env_file" -perm 600 -print)" = "$env_file"
grep -q '^MYSQL_DATABASE=bosapi$' "$env_file"
grep -q '^BOS_ENCRYPTION_KEY=' "$env_file"
grep -q '^BOS_ADMIN_EMAIL=admin@bosapi.local$' "$env_file"
grep -q '^BOS_ADMIN_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
! grep -q '^BOS_ADMIN_EMAIL=admin@bosapi.local$' "$test_root/init.log"
admin_password="$(sed -n 's/^BOS_ADMIN_PASSWORD=//p' "$env_file")"
! grep -Fxq -- "BOS_ADMIN_PASSWORD=$admin_password" "$test_root/init.log"
! grep -q '^DATABASE_HOST=' "$env_file"
test ! -s "$DOCKER_LOG"

rm "$env_file"
# Explicit source mode initializes credentials without a separate init command.
"$test_root/repo/deploy/stack.sh" up --source > "$test_root/first-up.log"
test -f "$env_file"
test "$(find "$env_file" -perm 600 -print)" = "$env_file"
grep -q '^MYSQL_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
grep -q '^MYSQL_ROOT_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
grep -q '^REDIS_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
grep -q '^BOS_ADMIN_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
grep -q '^BOS_JWT_SIGNING_KEY=[[:xdigit:]]\{64\}$' "$env_file"
grep -q '^BOS_ENCRYPTION_KEY=' "$env_file"
test "$(sed -n 's/^BOS_ENCRYPTION_KEY=//p' "$env_file" | openssl base64 -d -A | wc -c | tr -d ' ')" = 32
grep -q ' config --quiet$' "$DOCKER_LOG"
grep -q ' up -d --build --wait app$' "$DOCKER_LOG"
grep -q ' ps$' "$DOCKER_LOG"
head -n 2 "$DOCKER_LOG" > "$test_root/preflight-commands.log"
printf 'compose version\ninfo\n' > "$test_root/expected-preflight.log"
cmp "$test_root/expected-preflight.log" "$test_root/preflight-commands.log"
admin_password="$(sed -n 's/^BOS_ADMIN_PASSWORD=//p' "$env_file")"
! grep -Fq -- "$admin_password" "$test_root/first-up.log"
grep -q 'Initial administrator credentials:' "$test_root/first-up.log"

printf 'BOS_PRIMARY_DOMAIN=custom.example.com\n' >> "$env_file"
before="$(cksum "$env_file")"
chmod 644 "$env_file"
"$test_root/repo/deploy/stack.sh" init > "$test_root/reinit.log"
test "$(cksum "$env_file")" = "$before"
test "$(find "$env_file" -perm 600 -print)" = "$env_file"
! grep -q '^BOS_ADMIN_EMAIL=admin@bosapi.local$' "$test_root/reinit.log"
! grep -Fxq -- "BOS_ADMIN_PASSWORD=$admin_password" "$test_root/reinit.log"
chmod 644 "$env_file"
# The same source command preserves all private settings on subsequent updates.
"$test_root/repo/deploy/stack.sh" up --source > "$test_root/up.log"
test "$(cksum "$env_file")" = "$before"
test "$(find "$env_file" -perm 600 -print)" = "$env_file"
! grep -Fq -- "$admin_password" "$test_root/up.log"
grep -q ' up -d --build --wait app$' "$DOCKER_LOG"
! grep -q ' up -d --wait mysql redis$' "$DOCKER_LOG"

# Start reuses the configuration and image without building or rotating secrets.
cp "$env_file" "$test_root/start.env"
rm "$DOCKER_LOG"
"$test_root/repo/deploy/stack.sh" start > "$test_root/start.log"
cmp "$test_root/start.env" "$env_file"
grep -q ' config --quiet$' "$DOCKER_LOG"
grep -q ' up -d --no-build --wait app$' "$DOCKER_LOG"
grep -q ' ps$' "$DOCKER_LOG"
! grep -q -- '--build\|^compose .* build ' "$DOCKER_LOG"
! grep -Fq -- "$admin_password" "$test_root/start.log"

# Start must use the same optional monitor overlay as up.
mkdir -p "$test_root/repo/deploy/monitor"
touch "$test_root/repo/deploy/monitor.enabled" "$test_root/repo/deploy/monitor/compose.monitor.yml"
rm "$DOCKER_LOG"
"$test_root/repo/deploy/stack.sh" start > /dev/null
grep -q -- '-f .*/deploy/monitor/compose.monitor.yml up -d --no-build --wait app$' "$DOCKER_LOG"
rm "$test_root/repo/deploy/monitor.enabled"

# Invalid configuration or an unavailable image must stop before reporting status.
for failure in config missing-image; do
  rm "$DOCKER_LOG"
  if DOCKER_FAILURE_MODE="$failure" "$test_root/repo/deploy/stack.sh" start > "$test_root/start-failure.log" 2>&1; then
    printf 'start must reject %s\n' "$failure" >&2
    exit 1
  fi
  cmp "$test_root/start.env" "$env_file"
  ! grep -q ' ps$' "$DOCKER_LOG"
  if [[ "$failure" == config ]]; then
    ! grep -q ' up ' "$DOCKER_LOG"
  fi
done

# Upgrade an older private env file without rotating any service credentials.
sed '/^BOS_ADMIN_EMAIL=/d; /^BOS_ADMIN_PASSWORD=/d' "$env_file" > "$test_root/legacy.env"
cp "$test_root/legacy.env" "$env_file"
"$test_root/repo/deploy/stack.sh" up > "$test_root/legacy-up.log"
grep -q '^BOS_ADMIN_EMAIL=admin@bosapi.local$' "$env_file"
grep -q '^BOS_ADMIN_PASSWORD=[[:xdigit:]]\{48\}$' "$env_file"
test "$(find "$env_file" -perm 600 -print)" = "$env_file"
sed '/^BOS_ADMIN_EMAIL=/d; /^BOS_ADMIN_PASSWORD=/d; /^$/d' "$env_file" > "$test_root/preserved.env"
sed '/^$/d' "$test_root/legacy.env" > "$test_root/expected.env"
cmp "$test_root/expected.env" "$test_root/preserved.env"
before="$(cksum "$env_file")"
"$test_root/repo/deploy/stack.sh" up > /dev/null
test "$(cksum "$env_file")" = "$before"

# Empty dotenv values must fail after preflight without configuring or starting containers.
cp "$env_file" "$test_root/complete.env"
for field in BOS_ADMIN_EMAIL BOS_ADMIN_PASSWORD; do
  for empty_value in '' '   ' '""' "''" '# choose a value' '"" # choose a value'; do
    sed "/^$field=/d" "$test_root/complete.env" > "$env_file"
    printf '%s=%s\n' "$field" "$empty_value" >> "$env_file"
    cp "$env_file" "$test_root/empty.env"
    docker_before="$(wc -l < "$DOCKER_LOG")"
    if "$test_root/repo/deploy/stack.sh" up > "$test_root/empty.log" 2>&1; then
      printf 'empty %s must be rejected\n' "$field" >&2
      exit 1
    fi
    grep -q 'Set non-empty BOS_ADMIN_EMAIL and BOS_ADMIN_PASSWORD' "$test_root/empty.log"
    cmp "$test_root/empty.env" "$env_file"
    test "$(wc -l < "$DOCKER_LOG")" -eq "$((docker_before + 2))"
    tail -n 2 "$DOCKER_LOG" > "$test_root/preflight-commands.log"
    cmp "$test_root/expected-preflight.log" "$test_root/preflight-commands.log"
  done
done
cp "$test_root/complete.env" "$env_file"

# A partially supplied admin configuration must fail without changing the file.
sed '/^BOS_ADMIN_PASSWORD=/d' "$env_file" > "$test_root/partial.env"
cp "$test_root/partial.env" "$env_file"
if "$test_root/repo/deploy/stack.sh" init > "$test_root/partial.log" 2>&1; then
  printf 'partial administrator credentials must be rejected\n' >&2
  exit 1
fi
cmp "$test_root/partial.env" "$env_file"

# Restarting existing images still requires a prepared static root and
# preserves private credentials when its preflight fails.
cp "$test_root/complete.env" "$env_file"
cp "$env_file" "$test_root/web-before.env"
rm "$DOCKER_LOG"
if env BOS_WEB_DELIVERY=nginx BOS_WEB_ROOT="$test_root/missing-web-root" "$test_root/repo/deploy/stack.sh" start > "$test_root/web-missing.log" 2>&1; then
  printf 'start must reject an unprepared static root\n' >&2
  exit 1
fi
grep -q 'prepare a writable static root' "$test_root/web-missing.log"
cmp "$test_root/web-before.env" "$env_file"
! grep -q ' up ' "$DOCKER_LOG"

# Test mode parsing, publication integration, and failure reporting without
# depending on Docker execution or writing a real host static directory.
export REAL_PYTHON="$(command -v python3)"
cat > "$test_root/bin/python3" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PYTHON_LOG"
case "${PYTHON_FAILURE_MODE:-}:$*" in
  check:*' check-root '*|check:*' prepare-root '*|publish:*' publish '*) exit 1 ;;
esac
if [[ "$*" == *' prepare-root '* ]]; then exec "$REAL_PYTHON" "$@"; fi
EOF
chmod +x "$test_root/bin/python3"
export PYTHON_LOG="$test_root/python.log"
# A fresh source deployment creates its static directory before initializing
# credentials or starting containers, including when parent names contain spaces.
cp "$env_file" "$test_root/web-existing.env"
rm "$env_file" "$DOCKER_LOG"
fresh_root="$test_root/new host/static"
if env BOS_WEB_DELIVERY=nginx BOS_WEB_ROOT="$fresh_root" PYTHON_FAILURE_MODE=check "$test_root/repo/deploy/stack.sh" up --source > "$test_root/web-prepare-failed.log" 2>&1; then
  printf 'failed static root preparation must prevent initialization and startup\n' >&2
  exit 1
fi
test ! -e "$env_file"
test ! -e "$fresh_root"
! grep -q ' up ' "$DOCKER_LOG"
env BOS_WEB_DELIVERY=nginx BOS_WEB_ROOT="$fresh_root" "$test_root/repo/deploy/stack.sh" up --source > "$test_root/web-first-up.log"
test -d "$fresh_root"
test -w "$fresh_root"
test -f "$env_file"
grep -q " prepare-root --root $fresh_root$" "$PYTHON_LOG"
grep -q ' up -d --build --wait app$' "$DOCKER_LOG"
cp "$env_file" "$test_root/web-generated.env"
printf 'keep published files\n' > "$fresh_root/retained.txt"
env BOS_WEB_DELIVERY=nginx BOS_WEB_ROOT="$fresh_root" "$test_root/repo/deploy/stack.sh" up --source > "$test_root/web-repeat-up.log"
cmp "$test_root/web-generated.env" "$env_file"
grep -qx 'keep published files' "$fresh_root/retained.txt"
cp "$test_root/web-existing.env" "$env_file"

printf 'BOS_WEB_DELIVERY="nginx" # host static mode\nBOS_WEB_ROOT="%s"\n' "$test_root/host-web" >> "$env_file"
for command in up start publish-web; do
  rm -f "$DOCKER_LOG" "$PYTHON_LOG"
  env -u BOS_WEB_DELIVERY "$test_root/repo/deploy/stack.sh" "$command" > "$test_root/web-$command.log"
  if [[ "$command" == up ]]; then
    grep -q " prepare-root --root $test_root/host-web$" "$PYTHON_LOG"
    test -d "$test_root/host-web"
  else
    grep -q " check-root --root $test_root/host-web$" "$PYTHON_LOG"
  fi
  grep -q " publish --root $test_root/host-web --container app-container-id$" "$PYTHON_LOG"
  if [[ "$command" == publish-web ]]; then
    ! grep -q ' up ' "$DOCKER_LOG"
  else
    grep -q ' up ' "$DOCKER_LOG"
  fi
done
for failure in check publish; do
  rm -f "$DOCKER_LOG" "$PYTHON_LOG"
  if env -u BOS_WEB_DELIVERY PYTHON_FAILURE_MODE="$failure" "$test_root/repo/deploy/stack.sh" start > "$test_root/web-failure.log" 2>&1; then
    printf 'static publication must reject %s failure\n' "$failure" >&2
    exit 1
  fi
  if [[ "$failure" == check ]]; then
    ! grep -q ' up ' "$DOCKER_LOG"
  else
    grep -q ' up ' "$DOCKER_LOG"
    grep -q 'backend may already be updated' "$test_root/web-failure.log"
    ! grep -q ' ps$' "$DOCKER_LOG"
  fi
done
rm -f "$DOCKER_LOG" "$PYTHON_LOG"
BOS_WEB_DELIVERY=invalid "$test_root/repo/deploy/stack.sh" start > "$test_root/web-invalid.log" 2>&1 && exit 1
grep -q 'must be nginx or go' "$test_root/web-invalid.log"
! grep -q ' up ' "$DOCKER_LOG"
rm "$test_root/bin/python3"

printf 'PASS: local stack initialization, credential preservation and static publication\n'
