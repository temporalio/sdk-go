# Temporal Go SDK
[![Build Status](https://badge.buildkite.com/ce6df3b1a8b375270261ae70fb2d2756af298fef3a0dac4d20.svg?theme=github&branch=master)](https://buildkite.com/temporal/temporal-go-client) [![Coverage Status](https://coveralls.io/repos/github/temporalio/temporal-go-sdk/badge.svg?branch=master)](https://coveralls.io/github/temporalio/temporal-go-sdk?branch=master) [![PkgGoDev](https://pkg.go.dev/badge/go.temporal.io/sdk)](https://pkg.go.dev/go.temporal.io/sdk)

The Temporal Go SDK provides a framework for Temporal Application development in the Go language.
The SDK contains the following tools:

- A Temporal Client to communicate with a [Temporal Cluster](https://docs.temporal.io/docs/content/what-is-a-temporal-cluster)
- APIs to use within your Workflows
- APIs to create and manage [Workers](https://docs.temporal.io/docs/content/what-is-a-worker)

## How to use

Add the [Temporal Go SDK](https://github.com/temporalio/sdk-go) to your project:

```bash
go get -u go.temporal.io/sdk@latest
```

Or clone the Go SDK repo to your preferred location:

```bash
git clone git@github.com:temporalio/sdk-go.git
```
**Are there executable code samples?**

You can find a complete list of executable code samples in the [samples library](https://docs.temporal.io/docs/samples-library/#go), which includes Temporal Go SDK code samples from the [temporalio/samples-go](https://github.com/temporalio/samples-go) repo.
Additionally, each of the Go SDK Tutorials in the [Temporal docs site](https://docs.temporal.io/docs/go) is backed by a fully executable template application.

**Where is the Go SDK technical reference?**

The [Temporal Go SDK API reference](https://pkg.go.dev/go.temporal.io/sdk) is published on [pkg.go.dev](https://pkg.go.dev/go.temporal.io/sdk)

## How-to guides

- [How to develop a Workflow Definition](https://docs.temporal.io/docs/go/how-to-develop-a-workflow-definition-in-go)
- [How to develop an Activity Definition](https://docs.temporal.io/docs/go/how-to-develop-an-activity-definition-in-go)
- [How to develop a Worker Program](https://docs.temporal.io/docs/go/how-to-develop-a-worker-program-in-go)
- [How to spawn a Workflow Execution](https://docs.temporal.io/docs/go/how-to-spawn-a-workflow-execution-in-go)
- [How to spawn an Activity Execution](https://docs.temporal.io/docs/go/how-to-spawn-an-activity-execution-in-go)
- [How to spawn a Child Workflow Execution](https://docs.temporal.io/docs/go/how-to-spawn-a-child-workflow-execution-in-go)
- [How to execute a Side Effect](https://docs.temporal.io/docs/go/how-to-execute-a-side-effect-in-go)
- [How to get the result of a Workflow Execution](https://docs.temporal.io/docs/go/how-to-get-the-result-of-a-workflow-execution-in-go)
- [How to get the result of an Activity Execution](https://docs.temporal.io/docs/go/how-to-get-the-result-of-an-activity-execution-in-go)
- [How to send a Signal to a Workflow Execution](https://docs.temporal.io/docs/go/how-to-send-a-signal-to-a-workflow-execution-in-go)
- [How to handle a Signal in a Workflow](https://docs.temporal.io/docs/go/how-to-handle-a-signal-in-a-workflow-in-go)
- [How to send a Signal from within a Workflow](https://docs.temporal.io/docs/go/how-to-send-a-signal-to-a-workflow-execution-in-go)
- [How to Query a Workflow](https://docs.temporal.io/docs/go/queries)
- [How to set Options for the Worker](https://docs.temporal.io/docs/go/how-to-set-workeroptions-in-go)
- [How to execute a Side Effect](https://docs.temporal.io/docs/go/how-to-execute-a-side-effect-in-go)
- [How to Continue-As-New](https://docs.temporal.io/docs/go/how-to-continue-as-new-in-go)
- [How to set RegisterWorkflowOptions](https://docs.temporal.io/docs/go/how-to-set-registerworkflowoptions-in-go)
- [How to set SessionOptions](https://docs.temporal.io/docs/go/how-to-set-session-options-in-go)
- [How to set RegisterActivityOptions](https://docs.temporal.io/docs/go/how-to-set-registeractivityoptions-in-go)
- [How to set WorkerOptions](https://docs.temporal.io/docs/go/how-to-set-workeroptions-in-go)
- [How to set StartWorkflowOptions](https://docs.temporal.io/docs/go/how-to-set-startworkflowoptions-in-go)
- [How to set ActivityOptions](https://docs.temporal.io/docs/go/how-to-set-activityoptions-in-go)
- [How to test Workflow Definitions in Go](https://docs.temporal.io/docs/go/how-to-test-workflow-definitions-in-go)

## Workflow determinism checker

See (contrib/tools/workflowcheck)[contrib/tools/workflowcheck] for a tool to detect non-determinism in Workflow Definitions.

## Contributing

We'd love your help in making the Temporal Go SDK great. Please review our [contribution guidelines](CONTRIBUTING.md).

## License

MIT License, please see [LICENSE](LICENSE) for details.
