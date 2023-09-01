# Temporal Go SDK [![Build Status](https://badge.buildkite.com/ce6df3b1a8b375270261ae70fb2d2756af298fef3a0dac4d20.svg?theme=github&branch=master)](https://buildkite.com/temporal/temporal-go-client) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

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

## License
MIT License, please see [LICENSE](LICENSE) for details.
