# sdk-go test and verification modes

Use the Go version specified in `go.mod`. Prefer the canonical build tool and
run its commands from `internal/cmd/build`.

Focused unit test:

```text
go run . unit-test -run "TestName"
```

Focused integration test with an embedded dev server:

```text
go run . integration-test -dev-server -run "Suite/TestName"
```

If a compatible server is already listening on `localhost:7233`, omit
`-dev-server`. The workflow preloads the pinned embedded dev server before the
network-disabled investigation starts. Treat an unavailable required server
mode as missing verification, not a passing test.

Repository check:

```text
go run . check
```

For workflow execution, replay/cache, worker lifecycle, cancellation, update,
or local activity changes, run the focused integration test in both modes:

```text
go run . integration-test -dev-server -run "Suite/TestName"
WORKFLOW_CACHE_SIZE=0 go run . integration-test -dev-server -run "Suite/TestName"
```

Run a focused test at least five times before declaring a patch ready. Do not
turn this repetition into a committed retry. Record the exact command, attempt
count, and result in the structured response.

Prefer direct observation through channels, callbacks, history or event
polling, and existing helpers. Before adding `require.Eventually`, identify the
actual race and why polling the selected condition is correct. Never add an
arbitrary sleep to make timing appear stable.
