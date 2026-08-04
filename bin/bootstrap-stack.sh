#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
stack_dir="$repo_root/deploy/stack"
runtime_dir="${CPAMP_STACK_RUNTIME_DIR:-$stack_dir/runtime}"
secrets_dir="$runtime_dir/secrets"
env_file="$runtime_dir/.env"
start_stack=1

usage() {
  printf '%s\n' 'Usage: bin/bootstrap-stack.sh [--init-only]'
  printf '%s\n' '  --init-only  Generate private runtime files without starting Docker Compose.'
}

case "${1:-}" in
  "") ;;
  --init-only) start_stack=0 ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

umask 077
mkdir -p "$secrets_dir" \
  "$runtime_dir/cliproxyapi/auths" \
  "$runtime_dir/cliproxyapi/logs"
chmod 700 "$runtime_dir" "$secrets_dir" "$runtime_dir/cliproxyapi/auths" "$runtime_dir/cliproxyapi/logs"

if [ ! -f "$env_file" ]; then
  cp "$stack_dir/.env.example" "$env_file"
fi
chmod 600 "$env_file"

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
    return
  fi
  if command -v od >/dev/null 2>&1 && [ -r /dev/urandom ]; then
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    printf '\n'
    return
  fi
  printf '%s\n' 'openssl or od with /dev/urandom is required to generate secrets.' >&2
  exit 1
}

ensure_secret() {
  local path="$1"
  local prefix="${2:-}"
  local temporary
  if [ -s "$path" ]; then
    chmod 600 "$path"
    return
  fi
  temporary="${path}.tmp.$$"
  printf '%s%s\n' "$prefix" "$(random_hex)" >"$temporary"
  chmod 600 "$temporary"
  mv -f "$temporary" "$path"
}

ensure_secret "$secrets_dir/postgres-password"
ensure_secret "$secrets_dir/redis-password"
ensure_secret "$secrets_dir/cpamp-admin-password" 'Adm-'
ensure_secret "$secrets_dir/cpa-management-key" 'cpa_'
ensure_secret "$secrets_dir/cpamp-admin-key" 'cpamp_'
ensure_secret "$secrets_dir/cpamp-data-key"
ensure_secret "$secrets_dir/cpa-demo-client-key" 'sk-cpamp-'

IFS= read -r postgres_password <"$secrets_dir/postgres-password"
for dsn_name in core usage auth; do
  dsn_path="$secrets_dir/${dsn_name}-pg-dsn"
  dsn_temporary="${dsn_path}.tmp.$$"
  printf 'postgres://cpamp:%s@postgres:5432/cpamp?sslmode=disable\n' "$postgres_password" >"$dsn_temporary"
  chmod 600 "$dsn_temporary"
  mv -f "$dsn_temporary" "$dsn_path"
done
unset postgres_password

read_env_value() {
  local key="$1"
  awk -F= -v wanted="$key" '$1 == wanted { sub(/^[^=]*=/, ""); value=$0 } END { print value }' "$env_file"
}

console_origin="$(read_env_value CPAMP_CONSOLE_ORIGIN)"
api_origin="$(read_env_value CPAMP_PUBLIC_API_ORIGIN)"
console_origin="${console_origin:-http://localhost:18080}"
api_origin="${api_origin:-http://localhost:18318}"
if [[ ! "$console_origin" =~ ^https?://[][A-Za-z0-9._:-]+$ ]] ||
   [[ ! "$api_origin" =~ ^https?://[][A-Za-z0-9._:-]+$ ]]; then
  printf '%s\n' 'Console and API origins must be exact http(s) origins without a path.' >&2
  exit 1
fi

runtime_config="$runtime_dir/runtime-config.json"
runtime_config_tmp="${runtime_config}.tmp.$$"
{
  printf '{\n'
  printf '  "mode": "full",\n'
  printf '  "apiOrigin": "%s",\n' "$api_origin"
  printf '  "oauthReturnOrigin": "%s",\n' "$console_origin"
  printf '  "pluginRuntimeOrigin": "%s",\n' "$console_origin"
  printf '  "environmentName": "docker-stack"\n'
  printf '}\n'
} >"$runtime_config_tmp"
chmod 600 "$runtime_config_tmp"
mv -f "$runtime_config_tmp" "$runtime_config"

cpa_config="$runtime_dir/cliproxyapi/config.yaml"
if [ ! -s "$cpa_config" ]; then
  IFS= read -r cpa_management_key <"$secrets_dir/cpa-management-key"
  IFS= read -r demo_client_key <"$secrets_dir/cpa-demo-client-key"
  cpa_config_tmp="${cpa_config}.tmp.$$"
  {
    printf 'host: "0.0.0.0"\n'
    printf 'port: 8317\n\n'
    printf 'remote-management:\n'
    printf '  secret-key: "%s"\n' "$cpa_management_key"
    printf '  allow-remote: true\n'
    printf '  disable-control-panel: false\n'
    printf '  disable-auto-update-panel: true\n'
    printf '  panel-github-repository: "https://github.com/seakee/CPA-Manager-Plus"\n\n'
    printf 'usage-statistics-enabled: true\n'
    printf 'redis-usage-queue-retention-seconds: 60\n'
    printf 'auth-dir: "/root/.cli-proxy-api"\n\n'
    printf 'api-keys:\n'
    printf '  - "%s"\n' "$demo_client_key"
  } >"$cpa_config_tmp"
  chmod 600 "$cpa_config_tmp"
  mv -f "$cpa_config_tmp" "$cpa_config"
  unset cpa_management_key demo_client_key
fi
chmod 600 "$cpa_config"

printf '%s\n' "Private runtime configuration is ready: $runtime_dir"
printf '%s\n' "Administrator password file: $secrets_dir/cpamp-admin-password"
printf '%s\n' "Demo CPA client key file: $secrets_dir/cpa-demo-client-key"

if [ "$start_stack" -eq 0 ]; then
  printf '%s\n' 'Initialization completed without starting containers.'
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'docker is required to start the stack.' >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  printf '%s\n' 'Docker Compose v2 is required to start the stack.' >&2
  exit 1
fi

"$repo_root/bin/stack.sh" up -d --build
printf '%s\n' "Console: $console_origin"
printf '%s\n' "API: $api_origin"
