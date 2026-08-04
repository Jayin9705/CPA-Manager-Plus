#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
runtime_dir="${CPAMP_STACK_RUNTIME_DIR:-$repo_root/deploy/stack/runtime}"
env_file="$runtime_dir/.env"
compose_file="$repo_root/docker-compose.stack.yml"

if [ ! -s "$env_file" ]; then
  printf '%s\n' 'Stack runtime is not initialized. Run bin/bootstrap-stack.sh first.' >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  printf '%s\n' 'Docker with Compose v2 is required.' >&2
  exit 1
fi

export CPAMP_RUNTIME_DIR="$runtime_dir"
exec docker compose --env-file "$env_file" -f "$compose_file" "$@"
