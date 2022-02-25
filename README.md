# Temporal Go SDK [![Build Status](https://badge.buildkite.com/ce6df3b1a8b375270261ae70fb2d2756af298fef3a0dac4d20.svg?theme=github&branch=master)](https://buildkite.com/temporal/temporal-go-client) [![Coverage Status](https://coveralls.io/repos/github/temporalio/temporal-go-sdk/badge.svg?branch=master)](https://coveralls.io/github/temporalio/temporal-go-sdk?branch=master) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

[Temporal](https://github.com/temporalio/temporal) is a distributed, scalable, durable, and highly available orchestration engine used to execute asynchronous long-running business logic in a scalable and resilient way.

"Temporal Go SDK" is the framework for authoring workflows and activities using Go language.

## How to use

Clone this repo into the preferred location.

```bash
git clone git@github.com:temporalio/sdk-go.git
```

or

```bash
go get -u go.temporal.io/sdk
```

See [samples](https://github.com/temporalio/samples-go) to get started.

Documentation is available [here](https://docs.temporal.io). 
You can also find the API documentation [here](https://pkg.go.dev/go.temporal.io/sdk).

## Using a custom logger

Although the Go SDK does not support most third-party logging solutions natively, [our friends at Banzai Cloud](https://github.com/sagikazarmark) built the [adapter package logur](https://github.com/logur/logur) which makes it possible to use third party loggers with minimal overhead. Here is an example of using logur to support [Logrus](https://github.com/sirupsen/logrus):

```go
package main
import (
	"github.com/sirupsen/logrus"
	logrusadapter "logur.dev/adapter/logrus"
	"logur.dev/logur"
)

func main() {
	// feed this logger into Temporal
	logger := logur.LoggerToKV(logrusadapter.New(logrus.New()))
}
```

Most of the popular logging solutions have existing adapters in logur. If you're curious about which adapters are available, here is a helpful [link](https://github.com/logur?q=adapter-).

## Workflow determinism checker

See [contrib/tools/workflowcheck](contrib/tools/workflowcheck) for a tool to detect non-determinism in Workflow Definitions.

## Contributing
We'd love your help in making the Temporal Go SDK great. Please review our [contribution guidelines](CONTRIBUTING.md).

## License
MIT License, please see [LICENSE](LICENSE) for details.
