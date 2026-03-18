package serverlesslambdaworker

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

const (
	defaultMaxConcurrentActivityExecutionSize      = 10
	defaultMaxConcurrentWorkflowTaskExecutionSize  = 10
	defaultMaxConcurrentLocalActivityExecutionSize = 10
	defaultMaxConcurrentNexusTaskExecutionSize     = 5
	defaultMaxConcurrentActivityTaskPollers        = 1
	defaultMaxConcurrentWorkflowTaskPollers        = 2
	defaultMaxConcurrentNexusTaskPollers           = 1
	defaultWorkerStopTimeout                       = 5 * time.Second
	defaultStickyCacheSize                         = 100

	envTaskQueue       = "TEMPORAL_TASK_QUEUE"
	envFunctionName    = "AWS_LAMBDA_FUNCTION_NAME"
	envFunctionVersion = "AWS_LAMBDA_FUNCTION_VERSION"
)

// applyLambdaWorkerDefaults sets Lambda-appropriate defaults on the given worker options.
// Zero-valued fields are set to Lambda defaults; non-zero fields (previously set by envconfig or
// user) are left alone.
func applyLambdaWorkerDefaults(opts *worker.Options) {
	if opts.MaxConcurrentActivityExecutionSize == 0 {
		opts.MaxConcurrentActivityExecutionSize = defaultMaxConcurrentActivityExecutionSize
	}
	if opts.MaxConcurrentWorkflowTaskExecutionSize == 0 {
		opts.MaxConcurrentWorkflowTaskExecutionSize = defaultMaxConcurrentWorkflowTaskExecutionSize
	}
	if opts.MaxConcurrentLocalActivityExecutionSize == 0 {
		opts.MaxConcurrentLocalActivityExecutionSize = defaultMaxConcurrentLocalActivityExecutionSize
	}
	if opts.MaxConcurrentNexusTaskExecutionSize == 0 {
		opts.MaxConcurrentNexusTaskExecutionSize = defaultMaxConcurrentNexusTaskExecutionSize
	}
	if opts.MaxConcurrentActivityTaskPollers == 0 {
		opts.MaxConcurrentActivityTaskPollers = defaultMaxConcurrentActivityTaskPollers
	}
	if opts.MaxConcurrentWorkflowTaskPollers == 0 {
		opts.MaxConcurrentWorkflowTaskPollers = defaultMaxConcurrentWorkflowTaskPollers
	}
	if opts.MaxConcurrentNexusTaskPollers == 0 {
		opts.MaxConcurrentNexusTaskPollers = defaultMaxConcurrentNexusTaskPollers
	}
	if opts.WorkerStopTimeout == 0 {
		opts.WorkerStopTimeout = defaultWorkerStopTimeout
	}
	opts.DisableEagerActivities = true
}

// applyLambdaClientDefaults sets Lambda-appropriate defaults on the given client options:
// JSON-structured logging and a Lambda-aware identity string.
func applyLambdaClientDefaults(opts *client.Options, getenv func(string) string) {
	if opts.Logger == nil {
		opts.Logger = log.NewStructuredLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}
	if opts.Identity == "" {
		opts.Identity = buildLambdaIdentity(getenv)
	}
}

// buildLambdaIdentity constructs an identity string from Lambda environment variables in the form
// "lambda:<functionName>:<functionVersion>@<hostname>".
func buildLambdaIdentity(getenv func(string) string) string {
	functionName := getenv(envFunctionName)
	if functionName == "" {
		functionName = "unknown"
	}
	functionVersion := getenv(envFunctionVersion)
	if functionVersion == "" {
		functionVersion = "unknown"
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("lambda:%s:%s@%s", functionName, functionVersion, hostname)
}

// resolveTaskQueue determines the task queue name from user configuration or environment variables.
// Returns an error if no task queue is configured.
func resolveTaskQueue(userTaskQueue string, getenv func(string) string) (string, error) {
	if userTaskQueue != "" {
		return userTaskQueue, nil
	}
	if tq := getenv(envTaskQueue); tq != "" {
		return tq, nil
	}
	return "", fmt.Errorf("task queue not configured: set it via ConfigureWorkerContext.SetTaskQueue() or the %s environment variable", envTaskQueue)
}
