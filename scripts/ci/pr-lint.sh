#!/usr/bin/env bash
# Required PR formatting and Go lint contract.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# shellcheck source=../.buildflags
source "$REPO_ROOT/.buildflags"
# shellcheck source=lib/timing.sh
source "$REPO_ROOT/scripts/ci/lib/timing.sh"

cd "$REPO_ROOT"

status=0

ci_time_accumulate status "gofmt check" -- make fmt-check
ci_time_accumulate status "golangci-lint" -- \
    golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...

exit "$status"
