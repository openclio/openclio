#!/usr/bin/env sh
set -eu

echo "Running open-core boundary checks..."

# Public repository must not import private enterprise modules.
if command -v rg >/dev/null 2>&1; then
  if rg -n "github.com/.*/openclio-enterprise|internal/proprietary|internal/private" \
    cmd internal --glob "*.go"; then
    echo "Boundary check failed: private/proprietary import path found in public source."
    exit 1
  fi
else
  if grep -R -n -E "github.com/.*/openclio-enterprise|internal/proprietary|internal/private" \
    cmd internal --include="*.go"; then
    echo "Boundary check failed: private/proprietary import path found in public source."
    exit 1
  fi
fi

echo "Open-core boundary checks passed."
