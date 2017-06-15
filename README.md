# Go framework for Cadence
[Cadence](https://github.com/uber/cadence) is a distributed, scalable, durable, and highly available orchestration engine we developed at Uber Engineering to execute asynchronous long-running business logic in a scalable and resilient way.

`cadence-client` is the framework for authoring workflows and activites.

## How to use

Make sure you clone this repo into the correct location.

```bash
git clone git@github.com:uber/cadence.git $GOPATH/src/go.uber.org/cadence
```

or

```bash
go get go.uber.org/cadence
```

### Activity

Activity is the implementation of a particular task in the business logic. 

Activities are implemented as functions. Data can be passed directly to an activity via function parameters. The parameters can be either basic types or structs, with the only requirement being that the parameters need to be serializable. Even though it is not required, we recommand that the first parameter of an activity function is of type `context.Context`, in order to allow the activity to interact with other framework methods. The function must return an `error` value, and can optionally return a result value. The result value can be either a basic type or a struct with the only requirement being that the it is serializable.

The values passed to activities through invocation parameters or returned throughthe result value is recorded in the execution history. The entire execution history is transfered from the Cadence service to workflow workers with every event that the workflow logic needs to process. A large execution history can thus adversily impact the performance of your workflow. Therefore be mindful of the amount of data you transfer via activity invocation parameters or return values. Other than that no additional limitations exist on activity implementations.

In order to make the activity visible to the worker process hosting it, the activity needs to be registered via a call to `cadence.RegisterActivity`.

```go
package simple

import (
	"context"

	"go.uber.org/cadence"
	"go.uber.org/zap"
)

func init() {
	cadence.RegisterActivity(SimpleActivity)
}

// SimpleActivity is a sample Cadence activity function that takes one parameter and
// returns a string containing the parameter value.
func SimpleActivity(ctx context.Context, value string) (string, error) {
	cadence.GetActivityLogger(ctx).Info("SimpleActivity called.", zap.String("Value", value))
	return "Processed: " + value, nil
}
```

### Workflow

Workflow is the implementation of coordination logic. Its sole purpose is to orchestrate activity executions.

Workflows are implemented as functions. Startup data can be passed to a workflow via function parameters. The parameters can be either basic types or structs, with the only requirement being that the parameters need to be serializable. The first parameter of a workflow function is of type `cadence.Context`. The function must return an **error** value, and can optional return a result value. The result value can be either a basic type or a struct with the only requirement being that the it is serializable.

Workflow functions need to execute deterministically. Therefore, here is a list of rules that workflow code should obey to be a good Cadence citizen:
* Use `cadence.Context` everywhere.
* Don’t use range over `map`.
* Use `cadence.SideEffect` to call rand and similar nondeterministic functions like UUID generator.
* Use `cadence.Now` to get current time. Use `cadence.NewTimer` or `cadence.Sleep` instead of standard Go functions.
* Don’t use native channel and select. Use `cadence.Channel` and `cadence.Selector`.
* Don’t use go func(...). Use `cadence.Go(func(...))`.
* Don’t use non constant global variables as multiple instances of a workflow function can be executing in parallel.
* Don’t use any blocking functions besides belonging to `Channel`, `Selector` or `Future`
* Don’t use any synchronization primitives as they can cause blockage and there is no possibility of races when running under dispatcher.
* Don’t change workflow code when there are open workflows using it. Cadence is going to provide versioning mechanism to deal with deploying code changes without breaking existing workflows.
* Don’t perform any IO or service calls as they are not usually deterministic. Use activities for that.
* Don’t access configuration APIs directly from workflow as change in configuration affects workflow execution path. Either return configuration from an activity or use `cadence.SideEffect` to load it.

In order to make the workflow visible to the worker process hosting it, the workflow needs to be registered via a call to **cadence.RegisterWorkflow**.

```go
package simple

import (
	"time"

	"go.uber.org/cadence"
	"go.uber.org/zap"
)

func init() {
	cadence.RegisterWorkflow(SimpleWorkflow)
}

// SimpleWorkflow is a sample Cadence workflow that accepts one parameter and
// executes an activity to which it passes the aforementioned parameter.
func SimpleWorkflow(ctx cadence.Context, value string) error {
	options := cadence.ActivityOptions{
		ScheduleToStartTimeout: time.Second * 60,
		StartToCloseTimeout:    time.Second * 60,
	}
	ctx = cadence.WithActivityOptions(ctx, options)

	var result string
	err := cadence.ExecuteActivity(ctx, activity.SimpleActivity, value).Get(ctx, &result)
	if err != nil {
		return err
	}
	cadence.GetLogger(ctx).Info(
		"SimpleActivity returned successfully!", zap.String("Result", result))

	cadence.GetLogger(ctx).Info("SimpleWorkflow completed!")
	return nil
}
```

### Worker

A worker or “worker service” is a services hosting the workflow and activity implementations. The worker polls the “Cadence service” for tasks, performs those tasks and communicates task execution results back to the “Cadence service”. Worker services are developed, deployed and operated by Cadence customers.

You can run a Cadence worker in a new or an exiting service. Use the framework APIs to start the Cadence worker and link in all activity and workflow implementations that you require this service to execute.

```go
package main

import (
	"github.com/uber/tchannel-go"
	"github.com/uber/tchannel-go/thrift"

	"github.com/uber-go/tally"

	"go.uber.org/cadence"
	t "go.uber.org/cadence/.gen/go/cadence"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var HostPort = "127.0.0.1:7933"
var Domain = "SimpleDomain"
var ClientName = "SimpleWorker"
var CadenceService = "CadenceServiceFrontend"

func main() {
	startWorker(
		buildLogger(),
		buildCadenceClient())
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

func buildCadenceClient() t.TChanWorkflowService {
	tchan, err := tchannel.NewChannel(ClientName, nil)
	if err != nil {
		panic("Failed to setup channel")
	}

	opts := &thrift.ClientOptions{
		HostPort: HostPort,
	}
	return t.NewTChanWorkflowServiceClient(
		thrift.NewClient(tchan, CadenceService, opts))
}

func startWorker(logger *zap.Logger, client t.TChanWorkflowService) {
    // TaskListName - identifies set of client workflows, activities and workers.
    // it could be your group or client or application name.
	workerOptions := cadence.WorkerOptions{
		Logger:       logger,
		MetricsScope: tally.NewTestScope(TaskListName, map[string]string{}),
	}

	worker := cadence.NewWorker(
		client,
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
