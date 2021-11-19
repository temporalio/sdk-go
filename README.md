# Temporal Go SDK
[![Build Status](https://badge.buildkite.com/ce6df3b1a8b375270261ae70fb2d2756af298fef3a0dac4d20.svg?theme=github&branch=master)](https://buildkite.com/temporal/temporal-go-client) [![Coverage Status](https://coveralls.io/repos/github/temporalio/temporal-go-sdk/badge.svg?branch=master)](https://coveralls.io/github/temporalio/temporal-go-sdk?branch=master) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

The Temporal Go SDK provides a framework for Temporal Application development in the Go language.
The SDK contains the following tools:

- A Temporal Client to communicate with a Temporal Cluster
- APIs to use within your Workflows
- APIs to create and manage Worker Entities and Worker Processes

**How to use**

Add the [Temporal Go SDK](https://github.com/temporalio/sdk-go) to your project:

```bash
go get -u go.temporal.io/sdk@latest
```

Or clone the Go SDK repo to your preferred location:

```bash
git clone git@github.com:temporalio/sdk-go.git
```
**Are there executable code samples?**

You can find a complete list of executable code samples in the [samples library](/docs/samples-library/#go), which includes Temporal Go SDK code samples from the [temporalio/samples-go](https://github.com/temporalio/samples-go) repo.
Additionally, each of the Go SDK Tutorials in the [Temporal docs site](https://docs.temporal.io/docs/go) is backed by a fully executable template application.

**Where is the Go SDK technical reference?**

The [Temporal Go SDK API reference](https://pkg.go.dev/go.temporal.io/sdk) is published on [pkg.go.dev](https://pkg.go.dev/go.temporal.io/sdk)


**Links**

- [Contribution Guidelines](CONTRIBUTING.md)
- [Temporal Server repository](https://github.com/temporalio/temporal)
- [Temporal Documentation site](https://docs.temporal.io)
- [MIT License](LICENSE)

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
