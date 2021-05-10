#!/bin/sh

set -ex

# fetch codecov reporting tool
go get github.com/mattn/goveralls

# download cover files from all the tests
mkdir -p .build/coverage
buildkite-agent artifact download ".build/coverage/unit_test_cover.out" . --step ":golang: unit-test" --build "$BUILDKITE_BUILD_ID"
buildkite-agent artifact download ".build/coverage/integ_test_zero_cache_cover.out" . --step ":golang: integration-test-zero-cache" --build "$BUILDKITE_BUILD_ID"
buildkite-agent artifact download ".build/coverage/integ_test_normal_cache_cover.out" . --step ":golang: integration-test-normal-cache" --build "$BUILDKITE_BUILD_ID"

echo "download complete"

# report coverage
make cover_ci

# cleanup
rm -rf .build
