# Temporal Go SDK [![Build Status](https://github.com/temporalio/sdk-go/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/temporalio/sdk-go/actions/workflows/ci.yml) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

[Temporal](https://github.com/temporalio/temporal) is a distributed, scalable, durable, and highly available orchestration engine used to execute asynchronous long-running business logic in a scalable and resilient way.

"Temporal Go SDK" is the framework for authoring workflows and activities using Go language.

## How to use

Clone this repo into the preferred location.

```bash
git clone https://github.com/temporalio/sdk-go.git
```

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

## Go SDK upgrading past v1.25.1

Go SDK version v1.26.0 switched from using https://github.com/gogo/protobuf to https://github.com/golang/protobuf. While this migration is mostly internal there are a few user visible changes to be aware of:

### Change in types

* `time.Time` in proto structs will now be [timestamppb.Timestamp](https://pkg.go.dev/google.golang.org/protobuf@v1.31.0/types/known/timestamppb#section-documentation)
* `time.Duration` will now be [durationpb.Duration](https://pkg.go.dev/google.golang.org/protobuf/types/known/durationpb)
* V2-generated structs embed locks, so you cannot dereference them.

### Invalid UTF-8

Prior to SDK version v1.26.0 our protobuf code generator allowed invalid UTF-8 data to be stored as proto strings. This isn't actually allowed by the proto3 spec, so if you're using our SDK and think you may store arbitrary binary data in our strings you should set `-tags protolegacy` when building against our SDK.

Example:

``` shell
$ go build -tags protolegacy myworker/main.go
```

If you see an error like `grpc: error unmarshalling request: string field contains invalid UTF-8` then you will need to enable this when building your code.

If you're unsure then you should specify it anyways as there's no harm in doing so unless you relied on the protobuf compiler to ensure all strings were valid UTF-8.

### Incompatible proto/json encoding

Proto enums will, when formatted to JSON, now be in SCREAMING_SNAKE_CASE rather than PascalCase.
    * If trying to deserialize old JSON with PascalCase to proto use [go.temporal.io/api/temporalproto]

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
