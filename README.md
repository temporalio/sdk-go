![Temporal Go SDK](https://raw.githubusercontent.com/temporalio/assets/main/files/w/go.png)

# Temporal Go SDK [![Build Status](https://github.com/temporalio/sdk-go/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/temporalio/sdk-go/actions/workflows/ci.yml) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

[Temporal](https://github.com/temporalio/temporal) is a distributed, scalable, durable, and highly available orchestration engine used to execute asynchronous long-running business logic in a scalable and resilient way.

"Temporal Go SDK" is Temporal's framework for authoring workflows and activities using the Go language.

## Add the SDK to your project

The core Temporal Go SDK packages, including `client`, `worker`, and `workflow`,
are released together as the `go.temporal.io/sdk` module.

From your application's Go module, run:

```bash
go get go.temporal.io/sdk@latest
```

Then import the packages you need, such as:

```go
import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)
```

The integrations and tools under [`contrib`](contrib) are released as separate
Go modules. See each package's README for its module path and version.

## Getting started

See [samples](https://github.com/temporalio/samples-go) to get started.

Documentation is available [here](https://docs.temporal.io).
You can also find the API documentation [here](https://pkg.go.dev/go.temporal.io/sdk).

## Using slog

If using Go version 1.21+ the Go SDK provides built in integration with the standard [slog](https://pkg.go.dev/log) package.


```go
package main
import (
	"log/slog"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

func main() {
	clientOptions := client.Options{
		Logger: log.NewStructuredLogger(
			slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
				AddSource: true,
				Level:     slog.LevelDebug,
			}))),
	}
	temporalClient, err := client.Dial(clientOptions)
	// ...
}
```

## Workflow determinism checker

See [contrib/tools/workflowcheck](contrib/tools/workflowcheck) for a tool to detect non-determinism in Workflow Definitions.

## Contributing
We'd love your help in making the Temporal Go SDK great. Please review our [contribution guidelines](CONTRIBUTING.md).

## Development

### Prerequisites

- [Go](https://go.dev/) 1.24+ (see [go.mod](go.mod) for the minimum supported version)

### Local development workflow

The canonical local commands are provided by the build tool in `internal/cmd/build`.

Tests are managed through the build tool at `internal/cmd/build`. This tool handles starting an embedded Temporal dev
server with the required dynamic configs and search attributes, enforces consistent test flags (`-race`, `-count 1`, no caching),
and manages coverage collection, so you don't need to manually configure a server or remember the right flags.

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

### Running specific tests

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

### Coverage

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

### Go module housekeeping

If dependencies change, tidy all modules:

```bash
find . -name go.mod -execdir go mod tidy \;
```

### Pull request checklist

Before opening or updating a pull request:

- Run `go run . check` from `internal/cmd/build`.
- Run relevant tests (`unit-test` and, when needed, `integration-test -dev-server`).
- Keep changes focused and include tests for behavior changes.
- Update documentation/comments when public behavior changes.

## Go SDK upgrading past v1.25.1

Go SDK version v1.26.0 switched from using https://github.com/gogo/protobuf to https://github.com/golang/protobuf. While this migration is mostly internal there are a few user visible changes to be aware of:

### Change in types

* `time.Time` in proto structs will now be [timestamppb.Timestamp](https://pkg.go.dev/google.golang.org/protobuf@v1.31.0/types/known/timestamppb#section-documentation)
* `time.Duration` will now be [durationpb.Duration](https://pkg.go.dev/google.golang.org/protobuf/types/known/durationpb)
* V2-generated structs embed locks, so you cannot dereference them.

### Incompatible proto/json encoding

Proto enums will, when formatted to JSON, now be in SCREAMING_SNAKE_CASE rather than PascalCase.
    * If trying to deserialize old JSON with PascalCase to proto use [go.temporal.io/api/temporalproto](https://pkg.go.dev/go.temporal.io/api/temporalproto).

If users used Temporal proto types in their Workflows, such as for activity output, users may need to modify the default data converter to handle these payloads.
``` go
	converter.NewProtoJSONPayloadConverterWithOptions(converter.ProtoJSONPayloadConverterOptions{
		LegacyTemporalProtoCompat: true,
	}),
```

While upgrading from Go SDK version `< 1.26.0` to a version `>= 1.26.0` users may want to also bias towards using
proto binary to avoid any potential incompatibilities due to having clients serialize messages with incompatible `proto/json` format.

On clients running Go SDK `< 1.26.0`
``` go
converter.NewCompositeDataConverter(
		converter.NewNilPayloadConverter(),
		converter.NewByteSlicePayloadConverter(),
		converter.NewProtoPayloadConverter(),
		converter.NewProtoJSONPayloadConverterWithOptions(),
		converter.NewJSONPayloadConverter(),
	)
```

On clients running Go SDK `>= 1.26.0`

``` go
converter.NewCompositeDataConverter(
		converter.NewNilPayloadConverter(),
		converter.NewByteSlicePayloadConverter(),
		converter.NewProtoPayloadConverter(),
		converter.NewProtoJSONPayloadConverterWithOptions(converter.ProtoJSONPayloadConverterOptions{
			LegacyTemporalProtoCompat: true,
		}),
		converter.NewJSONPayloadConverter(),
	)
```

Note: Payloads encoded with `proto/binary` will not be readable in the Temporal web UI.

## License
MIT License, please see [LICENSE](LICENSE) for details.
