# Go framework for Cadence [![Build Status](https://travis-ci.org/uber-go/cadence-client.svg?branch=master)](https://travis-ci.org/uber-go/cadence-client) [![Coverage Status](https://coveralls.io/repos/uber-go/cadence-client/badge.svg?branch=master&service=github)](https://coveralls.io/github/uber-go/cadence-client?branch=master)
[Cadence](https://github.com/uber/cadence) is a distributed, scalable, durable, and highly available orchestration engine we developed at Uber Engineering to execute asynchronous long-running business logic in a scalable and resilient way.

`cadence-client` is the framework for authoring workflows and activites.

## How to use

Make sure you clone this repo into the correct location.

```bash
git clone git@github.com:uber-go/cadence-client.git $GOPATH/src/go.uber.org/cadence
```

or

```bash
go get go.uber.org/cadence
```

See [samples](https://github.com/samarabbas/cadence-samples) to get started

### Activity

Activity is the implementation of a particular task in the business logic.

Activities are implemented as functions. Data can be passed directly to an activity via function parameters. The parameters can be either basic types or structs, with the only requirement being that the parameters need to be serializable. Even though it is not required, we recommand that the first parameter of an activity function is of type `context.Context`, in order to allow the activity to interact with other framework methods. The function must return an `error` value, and can optionally return a result value. The result value can be either a basic type or a struct with the only requirement being that it is serializable.

The values passed to activities through invocation parameters or returned through the result value is recorded in the execution history. The entire execution history is transfered from the Cadence service to workflow workers with every event that the workflow logic needs to process. A large execution history can thus adversily impact the performance of your workflow. Therefore be mindful of the amount of data you transfer via activity invocation parameters or return values. Other than that no additional limitations exist on activity implementations.

In order to make the activity visible to the worker process hosting it, the activity needs to be registered via a call to `activity.Register`.

```go
package simple

import (
	"context"

    "go.uber.org/cadence/activity"
    "go.uber.org/zap"
)

func init() {
	activity.Register(SimpleActivity)
}

// SimpleActivity is a sample Cadence activity function that takes one parameter and
// returns a string containing the parameter value.
func SimpleActivity(ctx context.Context, value string) (string, error) {
	activity.GetLogger(ctx).Info("SimpleActivity called.", zap.String("Value", value))
	return "Processed: " + value, nil
}
```

### Workflow

Workflow is the implementation of coordination logic. Its sole purpose is to orchestrate activity executions.

Workflows are implemented as functions. Startup data can be passed to a workflow via function parameters. The parameters can be either basic types or structs, with the only requirement being that the parameters need to be serializable. The first parameter of a workflow function is of type `workflow.Context`. The function must return an **error** value, and can optional return a result value. The result value can be either a basic type or a struct with the only requirement being that the it is serializable.

Workflow functions need to execute deterministically. Therefore, here is a list of rules that workflow code should obey to be a good Cadence citizen:
* Use `workflow.Context` everywhere.
* Don’t use range over `map`.
* Use `workflow.SideEffect` to call rand and similar nondeterministic functions like UUID generator.
* Use `workflow.Now` to get current time. Use `workflow.NewTimer` or `workflow.Sleep` instead of standard Go functions.
* Don’t use native channel and select. Use `workflow.Channel` and `workflow.Selector`.
* Don’t use go func(...). Use `workflow.Go(func(...))`.
* Don’t use non constant global variables as multiple instances of a workflow function can be executing in parallel.
* Don’t use any blocking functions besides belonging to `Channel`, `Selector` or `Future`
* Don’t use any synchronization primitives as they can cause blockage and there is no possibility of races when running under dispatcher.
* Don't just change workflow code when there are open workflows. Always update code using `workflow.GetVersion`.
* Don’t perform any IO or service calls as they are not usually deterministic. Use activities for that.
* Don’t access configuration APIs directly from a workflow as changes in configuration will affect the workflow execution path. Either return configuration from an activity or use `workflow.SideEffect` to load it.

In order to make the workflow visible to the worker process hosting it, the workflow needs to be registered via a call to **workflow.Register**.

```go
package simple

import (
	"time"


	"go.uber.org/cadence/workflow"
	"go.uber.org/zap"
)

func init() {
	workflow.Register(SimpleWorkflow)
}

// SimpleWorkflow is a sample Cadence workflow that accepts one parameter and
// executes an activity to which it passes the aforementioned parameter.
func SimpleWorkflow(ctx workflow.Context, value string) error {
	options := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Second * 60,
		StartToCloseTimeout:    time.Second * 60,
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var result string
	err := workflow.ExecuteActivity(ctx, activity.SimpleActivity, value).Get(ctx, &result)
	if err != nil {
		return err
	}
	workflow.GetLogger(ctx).Info(
		"SimpleActivity returned successfully!", zap.String("Result", result))

	workflow.GetLogger(ctx).Info("SimpleWorkflow completed!")
	return nil
}
```

### Worker

A worker or “worker service” is a services hosting the workflow and activity implementations. The worker polls the “Cadence service” for tasks, performs those tasks and communicates task execution results back to the “Cadence service”. Worker services are developed, deployed and operated by Cadence customers.

You can run a Cadence worker in a new or an exiting service. Use the framework APIs to start the Cadence worker and link in all activity and workflow implementations that you require this service to execute.

```go
package main

import (

	t "go.uber.org/cadence/.gen/go/cadence"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/worker"

	"github.com/uber-go/tally"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/yarpc"
	"go.uber.org/yarpc/api/transport"
	"go.uber.org/yarpc/transport/tchannel"
)

var HostPort = "127.0.0.1:7933"
var Domain = "SimpleDomain"
var TaskListName = "SimpleWorker"
var ClientName = "SimpleWorker"
var CadenceService = "CadenceServiceFrontend"

func main() {
	startWorker(buildLogger(), buildCadenceClient())
}

func buildLogger() *zap.Logger {
	config := zap.NewDevelopmentConfig()
	config.Level.SetLevel(zapcore.InfoLevel)

	var err error
	logger, err := config.Build()
	if err != nil {
		panic("Failed to setup logger")
	}

	return logger
}

func buildCadenceClient() workflowserviceclient.Interface {
	ch, err := tchannel.NewChannelTransport(tchannel.ServiceName(ClientName))
	if err != nil {
		panic("Failed to setup tchannel")
	}
	dispatcher := yarpc.NewDispatcher(yarpc.Config{
			Name: ClientName,
			Outbounds: yarpc.Outbounds{
				CadenceService: {Unary: ch.NewSingleOutbound(HostPort)},
			},
		})
	if err := dispatcher.Start(); err != nil {
		panic("Failed to start dispatcher")
	}

	return workflowserviceclient.New(dispatcher.ClientConfig(CadenceService))
}

func startWorker(logger *zap.Logger, service workflowserviceclient.Interface) {
	// TaskListName - identifies set of client workflows, activities and workers.
	// it could be your group or client or application name.
	workerOptions := worker.Options{
		Logger:       logger,
		MetricsScope: tally.NewTestScope(TaskListName, map[string]string{}),
	}

	worker := worker.NewWorker(
		service,
		Domain,
		TaskListName,
		workerOptions)
	err := worker.Start()
	if err != nil {
		panic("Failed to start worker")
	}

	logger.Info("Started Worker.", zap.String("worker", TaskListName))
}
```

## Contributing
We'd love your help in making Cadence-client great. Please review our [instructions](CONTRIBUTING.md).

## License
MIT License, please see [LICENSE](LICENSE) for details.
