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
	defaultMaxConcurrentActivityExecutionSize      = 2
	defaultMaxConcurrentWorkflowTaskExecutionSize  = 10
	defaultMaxConcurrentLocalActivityExecutionSize = 2
	defaultMaxConcurrentNexusTaskExecutionSize     = 5
	defaultMaxConcurrentActivityTaskPollers        = 1
	defaultMaxConcurrentWorkflowTaskPollers        = 2
	defaultMaxConcurrentNexusTaskPollers           = 1
	defaultWorkerStopTimeout                       = 5 * time.Second
	defaultStickyCacheSize                         = 100

	envTaskQueue = "TEMPORAL_TASK_QUEUE"
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

// applyLambdaClientDefaults sets Lambda-appropriate defaults on the given client options that are
// available during the init phase (before any invocation). Identity is set later from the Lambda
// invocation context via [buildLambdaIdentity].
func applyLambdaClientDefaults(opts *client.Options) {
	if opts.Logger == nil {
		opts.Logger = log.NewStructuredLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}
}

// buildLambdaIdentity constructs an identity string in the form "<requestID>@<functionARN>" from
// the Lambda invocation context.
func buildLambdaIdentity(requestID, functionARN string) string {
	if requestID == "" {
		requestID = "unknown"
	}
	if functionARN == "" {
		functionARN = "unknown"
	}
	return fmt.Sprintf("%s@%s", requestID, functionARN)
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
