#!/usr/bin/env sh
# Cross-compiles PowerDistributionSystem.exe from Linux/macOS (requires Go 1.24+).
# Usage: build/build-windows.sh [version]
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-1.0.0}"
mkdir -p dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=$VERSION" \
  -o dist/PowerDistributionSystem.exe ./cmd/pds
echo "Built dist/PowerDistributionSystem.exe ($VERSION)"
