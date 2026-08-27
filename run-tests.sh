#!/usr/bin/env bash
# Runs the SAA operator-command tests on gmt/operator-commands-steps.
#
# No server setup: `-dev-server` makes the build tool download and start the CLI release pinned
# in internal/cmd/build/main.go — the same path CI takes.
#
# NOTE: not added to git (per working conventions).
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/internal/cmd/build"

go run . integration-test -dev-server -run 'TestIntegrationSuite/TestActivityOperatorCommandsSuite'
