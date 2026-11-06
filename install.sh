#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -gt 0 && "$1" != --source ]]; then
  printf 'Usage: ./install.sh [--source]\nRun this script from a source checkout.\n' >&2
  exit 2
fi
if [[ $# -gt 1 ]]; then exit 2; fi
exec bash "$repo_dir/deploy/stack.sh" up --source
