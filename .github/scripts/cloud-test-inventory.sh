#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "usage: $0 [requires_local_server|requires_cloud_provisioning|needs_cloud_adaptation]" >&2
}

if [[ $# -gt 1 ]]; then
  usage
  exit 2
fi

case "${1:-all}" in
  all)
    marker='cloud(RequiresLocalServer|RequiresProvisioning|NeedsAdaptation)'
    ;;
  requires_local_server)
    marker='cloudRequiresLocalServer'
    ;;
  requires_cloud_provisioning)
    marker='cloudRequiresProvisioning'
    ;;
  needs_cloud_adaptation)
    marker='cloudNeedsAdaptation'
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage
    exit 2
    ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

pattern="^[[:space:]]*skipOnCloud\\([^,]+,[[:space:]]*${marker},"
rg --line-number --no-heading --glob '*_test.go' "$pattern" test || {
  status=$?
  if [[ $status -ne 1 ]]; then
    exit "$status"
  fi
}
