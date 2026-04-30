# Contributing to Temporal Go SDK

This doc is intended for contributors to Go SDK (hopefully that's you!)

All contributors must complete the Temporal Contributor License Agreement (CLA) before changes can be merged. A link to the CLA will be posted in the PR.

## Prerequisites

- [Go](https://go.dev/) 1.24+ (see [go.mod](go.mod) for the minimum supported version)

## Local development workflow

The canonical local commands are provided by the build tool in `internal/cmd/build`.

Tests are managed through the build tool at `internal/cmd/build`. This tool handles starting an embedded Temporal dev
server with the required dynamic configs and search attributes, enforces consistent test flags (`-race`, `-count 1`, no caching),
and manages coverage collection — so you don't need to manually configure a server or remember the right flags.

```bash
cd internal/cmd/build
```

Run static analysis checks:

```bash
go run . check
```

Run unit tests (all packages except `test/`):

```bash
go run . unit-test
```

Run integration tests with an embedded Temporal dev server:

```bash
go run . integration-test -dev-server
```

If you omit `-dev-server`, integration tests connect to a server already running on `localhost:7233`.

## Running specific tests

Use `-run` with the same semantics as `go test -run`.

Unit tests:

```bash
go run . unit-test -run "TestMyFunction"
```

Integration tests:

```bash
# Single test in a suite
go run . integration-test -dev-server -run "TestIntegrationSuite/TestMyTest"

# Entire suite
go run . integration-test -dev-server -run "TestWorkerTunerTestSuite"
```

## Coverage

Unit test coverage (writes per-package profiles under `.build/coverage`):

```bash
go run . unit-test -coverage
```

Integration test coverage:

```bash
go run . integration-test -dev-server -coverage-file integration-test.out
```

Merge coverage files:

```bash
go run . merge-coverage-files coverage.out
```

## Go module housekeeping

If dependencies change, tidy all modules:

```bash
find . -name go.mod -execdir go mod tidy \;
```

## Pull request checklist

Before opening or updating a pull request:

- Run `go run . check` from `internal/cmd/build`.
- Run relevant tests (`unit-test` and, when needed, `integration-test -dev-server`).
- Keep changes focused and include tests for behavior changes.
- Update documentation/comments when public behavior changes.
