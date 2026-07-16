# Contributor Guidance for `sdk-go`

This repository provides the Go SDK for Temporal. Use this document as the
quick reference for coding-agent work and pull requests.

## Requirements for coding agents

- Use the Go requirement specified in `go.mod`.
- Prefer the canonical build tool in `internal/cmd/build` for checks and tests.
  It supplies the repo's expected flags, race detector, uncached test runs,
  coverage wiring, embedded dev server setup, dynamic config, and search
  attributes. Do not hand-roll equivalent integration-test commands unless you
  have a specific reason.
- Run build-tool commands from `internal/cmd/build`:
  - `go run . check`
  - `go run . unit-test`
  - `go run . integration-test -dev-server`
- For focused tests, use the build tool's `-run` flag:
  - `go run . unit-test -run "TestMyFunction"`
  - `go run . integration-test -dev-server -run "TestIntegrationSuite/TestMyTest"`
- If you need integration tests against an already-running server on
  `localhost:7233`, omit `-dev-server`. Otherwise prefer `-dev-server`.
- Avoid fixed sleeps in tests. Prefer channels, callbacks, history/event
  polling, `require.Eventually` with a known race, or existing test helpers that
  observe the condition directly.
- Before wrapping assertions in `Eventually`, understand the race. Check whether
  work has actually started, whether cleanup is asynchronous, and whether the
  failure is from stale CI or a known server-side timing issue.
- Keep comments sparse. Comments should explain non-obvious reasons or
  invariants, not restate what the code already says.
- Error messages should be actionable and specific. Include the operation,
  option, input, or state that helps the caller diagnose the problem.
- Do not add abstractions for simple one-off helpers. Follow the style already
  used in the package you are changing.
- Preserve public API compatibility and documented behavior unless a breaking
  change is explicit and reviewed. User-facing changes, including new features,
  behavior changes, deprecations, breaking changes, notable bug fixes, and
  security fixes, require a `CHANGELOG.md` entry under `## [Unreleased]`.
  Use the affected module's `CHANGELOG.md` for independently released `contrib`
  modules and the repository-root `CHANGELOG.md` for the main SDK module.
- Behavior changes require tests. Public API or public behavior changes also
  need documentation or examples when existing user-facing guidance would become
  incomplete or misleading.
- If dependencies change, tidy all modules from the repository root with:
  `find . -name go.mod -execdir go mod tidy \;`
- Treat generated files, mocks, and module metadata carefully. Regenerate with
  existing repo tooling when available, and avoid unrelated churn.
- Do not hand-edit generated mocks or generated protocol artifacts unless the
  file is explicitly maintained with manual fixups. Inspect file headers and use
  the local generator path for that file.

## Building and testing

The canonical local workflow is described in `CONTRIBUTING.md`.

```bash
cd internal/cmd/build
go run . check
go run . unit-test
go run . integration-test -dev-server
```

Useful focused commands:

```bash
cd internal/cmd/build
go run . unit-test -run "TestPayloadLimitsVisitor"
go run . integration-test -dev-server -run "TestWorkerTunerTestSuite"
go run . integration-test -dev-server -run "TestIntegrationSuite/TestShutdownDuringActiveTimerActivityWorkflows"
```

Coverage:

```bash
cd internal/cmd/build
go run . unit-test -coverage
go run . integration-test -dev-server -coverage-file integration-test.out
go run . merge-coverage-files coverage.out
```

Use direct `go test` for very small compile or package checks when that is the
right tool, but prefer the build tool before considering a change verified.

Prefer tests through exported APIs when feasible, especially for public SDK
behavior. Use internal hooks only when the behavior cannot be observed
externally.

## Go SDK review and implementation focus

- Workflow code must remain deterministic. Be careful with time, goroutines,
  channels, maps, random values, logging side effects, and any code reachable
  from workflow execution.
- For workflow state-machine, command generation, marker, update, local
  activity, Nexus workflow behavior, or history compatibility changes, run or
  add replay coverage under `test/replaytests` and use workflow replayer checks
  to protect existing histories.
- For cross-SDK behavior, compare the actual Java, Rust, TypeScript, and Python
  implementations and tests when practical. Prefer idiomatic Go SDK design over
  literal ports.
- When an interface or callback has ownership, lifetime, concurrency, or
  mutation requirements, document that contract at the interface boundary.
- For shutdown, poller, or worker lifecycle changes, reason through poll RPC
  lifecycle, `noRepoll`, dispatcher drain, task limiters, `WorkerStopTimeout`,
  activity and workflow workers, Nexus workers, sessions, and both legacy and
  current server behavior.
- Do not assume normal workers and session workers are equivalent. Session
  queues are activity-only.
- Be precise about Nexus terminology. Nexus workers and Nexus operations are not
  activities.
- For server-dependent tests, confirm the embedded dev server dynamic config is
  sufficient before blaming SDK logic.
- When diagnosing CI, compare the failing run SHA to the current PR head before
  calling something a regression.

## Pull request expectations

- Keep changes focused and include tests for behavior changes.
- Run `go run . check` from `internal/cmd/build`.
- Run relevant unit tests, and integration tests with `-dev-server` when the
  change touches workflow execution, workers, clients, Nexus, payload handling,
  interceptors, or server interaction.
- For workflow execution, replay/cache, worker lifecycle, cancellation, update,
  or local activity changes, run focused integration tests in both default-cache
  mode and with `WORKFLOW_CACHE_SIZE=0`.
- Update public docs, examples, and the affected module's `CHANGELOG.md` for
  user-facing changes.
- Keep commit messages short and imperative.

## Where things are

- `client/` - public client API.
- `worker/` - public worker API.
- `workflow/` - public workflow API and workflow-facing helpers.
- `activity/` - public activity API.
- `internal/` - core SDK implementation, state machines, task handlers,
  workers, converters, payload limits, sessions, and protocol internals.
- `internal/cmd/build/` - canonical local check/test runner.
- `test/` - integration test suite run by `integration-test`.
- `testsuite/` - SDK test environment and dev-server helpers.
- `converter/` - data converters, payload codecs, failure conversion, and gRPC
  payload interception.
- `interceptor/` - tracing and interceptor implementations.
- `temporalnexus/` - Temporal Nexus operation APIs and helpers.
- `contrib/` - optional integrations such as OpenTelemetry, OpenTracing, Tally,
  Datadog, envconfig, sysinfo, and workflow streams.
- `mocks/` - generated or maintained mocks used by tests.
- `CHANGELOG.md` - user-facing release notes for the main SDK module. Each
  independently released `contrib` module has its own `CHANGELOG.md`.

## Notes

- Protobuf-facing behavior may be visible to users. The README documents the
  gogo/protobuf to golang/protobuf migration and proto JSON compatibility
  concerns; keep those compatibility notes in mind when changing converters or
  encoded payload behavior.
- When the integration-test dev server uses a custom version, prefer the build
  tool so tests run against the intended server shape.
- `test/` is its own Go module. Several `contrib/*` directories are also Go
  modules. Remember module boundaries when running tests or tidying.
