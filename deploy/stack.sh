#!/usr/bin/env bash
set -euo pipefail

deploy_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$deploy_dir/.." && pwd)"
env_file="$deploy_dir/stack.env"
compose_file="$deploy_dir/docker-compose.yml"
image_env_file="$deploy_dir/image.env"
temporary_image_env=""

cleanup() {
  [[ -z "$temporary_image_env" ]] || rm -f "$temporary_image_env"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: ./deploy/stack.sh COMMAND [OPTIONS]

  init             Generate credentials or fill missing deployment defaults; preserve existing values
  up               Build source, or reuse the pinned image when image.env exists
  up --source      Build this working tree and switch to source deployment
  up --image REF   Pull REF and switch to that immutable local image
  start            Start or update the stack using existing configuration and images
  publish-web      Publish the current app image's frontend when static mode is enabled
  status           Show the stack status
  logs             Follow application logs
  down             Stop the stack without deleting its data volumes

All commands operate on Docker on this machine. They never connect to a remote host.
up initializes stack.env when missing and reuses existing credentials on updates.
Running init separately is optional; up creates a missing Nginx static root.
up never fetches Git. Only up --image REF pulls a new application image.
EOF
}

env_setting() {
  # Read only simple deployment settings; never execute the private env file.
  local key="$1" line value="$2" file="${3:-$env_file}"
  if [[ -f "$file" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" == "$key="* ]]; then
        value="${line#*=}"
        value="${value%$'\r'}"
        value="${value%% #*}"
        value="${value#"${value%%[![:space:]]*}"}"
        value="${value%"${value##*[![:space:]]}"}"
        if [[ "$value" == \"*\" || "$value" == \'*\' ]]; then
          value="${value:1:${#value}-2}"
        fi
      fi
    done < "$file"
  fi
  printf '%s' "$value"
}

web_mode() {
  printf '%s' "${BOS_WEB_DELIVERY-$(env_setting BOS_WEB_DELIVERY go)}"
}

web_root() {
  printf '%s' "${BOS_WEB_ROOT-$(env_setting BOS_WEB_ROOT /var/www/bosapi)}"
}

check_web_publication() {
  case "$(web_mode)" in
    go) return 0 ;;
    nginx) ;;
    *) printf 'BOS_WEB_DELIVERY must be nginx or go.\n' >&2; exit 1 ;;
  esac
  if ! command -v python3 >/dev/null 2>&1; then
    printf 'Static publication requires Python 3.9 or newer.\n' >&2
    exit 1
  fi
  python3 "$deploy_dir/publish-web.py" "${1:-check-root}" --root "$(web_root)"
}

app_container() {
  compose ps -q app
}

publish_web() {
  [[ "$(web_mode)" == go ]] && return 0
  local container
  container="$(app_container)"
  if [[ -z "$container" || "$container" == *$'\n'* ]]; then
    printf 'Static publication requires exactly one app container.\n' >&2
    exit 1
  fi
  if ! python3 "$deploy_dir/publish-web.py" publish --root "$(web_root)" --container "$container"; then
    printf 'The backend may already be updated; the current frontend was not intentionally switched. Inspect current before retrying.\n' >&2
    exit 1
  fi
}

compose_args() {
	compose_options=(--env-file "$env_file" -f "$compose_file")
	if [[ "$deployment_mode" == image ]]; then
		compose_options+=(--env-file "$image_env_file" -f "$deploy_dir/compose.image.yml")
	fi
	if [[ -f "$deploy_dir/monitor.enabled" ]]; then
		compose_options+=(-f "$deploy_dir/monitor/compose.monitor.yml")
	fi
	if [[ -f "$deploy_dir/https.enabled" ]]; then
		compose_options+=(-f "$deploy_dir/compose.https.yml")
	fi
}

compose() {
  compose_args
	# A shell variable must not override the image selected by the deployment file.
	(unset BOS_APP_IMAGE; COMPOSE_PROGRESS="${COMPOSE_PROGRESS:-plain}" docker compose "${compose_options[@]}" "$@")
}


show_deployment_mode() {
  if [[ "$deployment_mode" == source ]]; then
    printf 'Deployment mode: source (current working tree; Git is not fetched).\n'
  else
    printf 'Deployment mode: image (%s; no app build or pull during startup).\n' "$(env_setting BOS_APP_IMAGE unknown "$image_env_file")"
    printf 'To update: use up --source, or up --image REFERENCE to pull another image.\n'
  fi
}

show_running_version() {
  local container image
  if container="$(app_container)" && [[ -n "$container" && "$container" != *$'\n'* ]] &&
      image="$(docker inspect --format '{{.Image}}' "$container" 2>/dev/null)" && [[ -n "$image" ]]; then
    docker image inspect --format 'Running image: {{.Id}}
Version: {{index .Config.Labels "org.opencontainers.image.version"}}
Revision: {{index .Config.Labels "org.opencontainers.image.revision"}}' "$image" && return 0
  fi
  printf 'Running version metadata is unavailable.\n'
}

prepare_image() {
  local identity
  printf 'Pulling application image: %s\n' "$image_reference"
  docker pull "$image_reference"
  identity="$(docker image inspect --format '{{.Id}}' "$image_reference")"
  if [[ ! "$identity" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    printf 'Cannot resolve the pulled application image to an immutable image ID.\n' >&2
    exit 1
  fi
  temporary_image_env="$(mktemp "$deploy_dir/.image-deploy.XXXXXX")"
  chmod 600 "$temporary_image_env"
  printf 'BOS_APP_IMAGE=%s\n' "$identity" > "$temporary_image_env"
  image_env_file="$temporary_image_env"
}

persist_deployment_mode() {
  # Persist only after backend readiness, before publishing its frontend. A
  # publication failure must leave the mode consistent with the running backend.
  if [[ "$requested_mode" == source ]]; then
    rm -f "$deploy_dir/image.env"
  elif [[ -n "$temporary_image_env" ]]; then
    mv -f "$temporary_image_env" "$deploy_dir/image.env"
    temporary_image_env=""
    image_env_file="$deploy_dir/image.env"
  fi
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    printf 'Docker is not installed or not on PATH. Install Docker and the Docker Compose v2 plugin before running up.\n' >&2
    exit 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    printf 'Docker Compose v2 is unavailable. Install or update the Docker Compose v2 plugin before running up.\n' >&2
    exit 1
  fi
  if ! docker info >/dev/null; then
    printf 'Cannot access the Docker daemon. Start Docker and ensure the current user has permission to access it before running up.\n' >&2
    exit 1
  fi
}

init_env() {
  if [[ -e "$env_file" ]]; then
    printf 'Using existing %s\n' "$env_file"
    ensure_admin_env
    return
  fi

  umask 077
  local mysql_password mysql_root_password redis_password jwt_key encryption_key admin_password
  mysql_password="$(openssl rand -hex 24)"
  mysql_root_password="$(openssl rand -hex 24)"
  redis_password="$(openssl rand -hex 24)"
  jwt_key="$(openssl rand -hex 32)"
  encryption_key="$(openssl rand -base64 32)"
  admin_password="$(openssl rand -hex 24)"
  cat > "$env_file" <<EOF
BOSAPI_PORT=18080
BOS_WEB_DELIVERY=go
BOS_WEB_ROOT=/var/www/bosapi
MYSQL_DATABASE=bosapi
MYSQL_USER=bosapi
MYSQL_PASSWORD=$mysql_password
MYSQL_ROOT_PASSWORD=$mysql_root_password
REDIS_PASSWORD=$redis_password
BOS_JWT_SIGNING_KEY=$jwt_key
BOS_ENCRYPTION_KEY=$encryption_key
BOS_ADMIN_EMAIL=admin@bosapi.local
BOS_ADMIN_PASSWORD=$admin_password
BOS_ALLOWED_UPSTREAM_HOSTS=
# Operator-supplied client required for Gemini subscription sign-in and refresh.
BOS_GEMINI_OAUTH_CLIENT_ID=
BOS_GEMINI_OAUTH_CLIENT_SECRET=
# Operator-supplied client required for Antigravity subscription sign-in and refresh.
BOS_ANTIGRAVITY_OAUTH_CLIENT_ID=
BOS_ANTIGRAVITY_OAUTH_CLIENT_SECRET=
TZ=Asia/Shanghai
EOF
  printf 'Created %s (mode 600)\n' "$env_file"
  printf 'Keep %s and its encryption key across restarts.\n' "$env_file"
  printf 'Initial administrator credentials: BOS_ADMIN_EMAIL and BOS_ADMIN_PASSWORD in %s\n' "$env_file"
}


ensure_admin_env() {
  local has_email=0 has_password=0 admin_password temporary
  chmod 600 "$env_file"
  grep -q '^BOS_ADMIN_EMAIL=' "$env_file" && has_email=1
  grep -q '^BOS_ADMIN_PASSWORD=' "$env_file" && has_password=1
  if [[ "$has_email" == 1 && "$has_password" == 1 ]]; then
    if grep -Eq "^BOS_ADMIN_(EMAIL|PASSWORD)=[[:space:]]*(\"\"|'')?[[:space:]]*(#.*)?$" "$env_file"; then
      printf 'Set non-empty BOS_ADMIN_EMAIL and BOS_ADMIN_PASSWORD in %s.\n' "$env_file" >&2
      exit 1
    fi
    return
  fi
  if [[ "$has_email" != "$has_password" ]]; then
    printf 'Set both BOS_ADMIN_EMAIL and BOS_ADMIN_PASSWORD in %s.\n' "$env_file" >&2
    exit 1
  fi
  admin_password="$(openssl rand -hex 24)"
  temporary="$(mktemp "$env_file.XXXXXX")"
  cat "$env_file" > "$temporary"
  printf '\nBOS_ADMIN_EMAIL=admin@bosapi.local\nBOS_ADMIN_PASSWORD=%s\n' "$admin_password" >> "$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$env_file"
  printf 'Added initial administrator credentials to %s; existing service credentials are preserved.\n' "$env_file"
}

require_env() {
  if [[ ! -f "$env_file" ]]; then
    printf 'Missing %s; restore the existing stack environment file before starting.\n' "$env_file" >&2
    exit 1
  fi
  compose config --quiet
}

command="${1:-}"
[[ $# -eq 0 ]] || shift
requested_mode=auto
image_reference=""
case "$command" in
  up)
    if [[ "$command" == up && $# -eq 1 && "$1" == --source ]]; then
      requested_mode=source
    elif [[ $# -eq 2 && "$1" == --image ]]; then
      requested_mode=image
      image_reference="$2"
      if [[ ! "$image_reference" =~ ^[A-Za-z0-9][A-Za-z0-9._:/@-]*$ || "$image_reference" == *://* ]]; then
        printf 'Provide a Docker image reference, such as registry.example.com/team/bosapi:VERSION.\n' >&2
        exit 2
      fi
    elif [[ $# -ne 0 ]]; then
      usage >&2
      exit 2
    fi
    ;;
  *)
    if [[ $# -ne 0 ]]; then
      usage >&2
      exit 2
    fi
    ;;
esac
deployment_mode=source
if [[ "$requested_mode" == image || ( "$requested_mode" == auto && -f "$image_env_file" ) ]]; then
  deployment_mode=image
fi

cd "$repo_dir"
services=(app)
if [[ -f "$deploy_dir/https.enabled" ]]; then
  services+=(caddy)
fi
case "$command" in
  -h|--help)
    usage
    ;;
  init)
    init_env
    printf 'Administrator credentials are stored privately in %s.\n' "$env_file"
    ;;
  up)
    require_docker
    check_web_publication prepare-root
    init_env
    if [[ "$requested_mode" == image ]]; then
      prepare_image
    fi
    require_env
    show_deployment_mode
    if [[ "$deployment_mode" == image ]]; then
      image_start_args=(up -d --no-build)
      # An explicit image deployment may need to pull missing dependencies.
      # The app itself remains pinned by compose.image.yml's pull_policy.
      if [[ "$requested_mode" != image ]]; then
        image_start_args+=(--pull never)
      fi
      if ! compose "${image_start_args[@]}" --wait "${services[@]}"; then
        printf 'Application startup failed; the saved image selection was not changed. Inspect the running app and migrations before retrying.\n' >&2
        exit 1
      fi
    else
      if ! compose up -d --build --wait "${services[@]}"; then
        printf 'Source build or startup failed; the saved image selection was not changed. Inspect the running app and migrations before retrying.\n' >&2
        exit 1
      fi
    fi
    persist_deployment_mode
    publish_web
    compose ps
    show_running_version
    printf 'Initial administrator credentials: BOS_ADMIN_EMAIL and BOS_ADMIN_PASSWORD in %s\n' "$env_file"
    printf 'MySQL stores the Argon2id password hash; redeployment does not reset existing administrators.\n'
    ;;
  start)
    require_docker
    check_web_publication
    require_env
    show_deployment_mode
    compose up -d --no-build --wait "${services[@]}"
    publish_web
    compose ps
    show_running_version
    ;;
  publish-web)
    require_docker
    require_env
    if [[ "$(web_mode)" == go ]]; then
      printf 'Set BOS_WEB_DELIVERY=go and prepare BOS_WEB_ROOT before publishing.\n' >&2
      exit 1
    fi
    check_web_publication
    publish_web
    ;;
  status)
    require_env
    show_deployment_mode
    compose ps
    show_running_version
    ;;
  logs)
    require_env
    compose logs -f "${services[@]}"
    ;;
  down)
    require_env
    compose down
    ;;
  *)
    usage
    exit 2
    ;;
esac
