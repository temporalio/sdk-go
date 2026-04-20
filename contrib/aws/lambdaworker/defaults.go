package lambdaworker

import (
	"fmt"
	"log/slog"
	"os"
	"path"
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
	defaultShutdownHookBuffer                      = 2 * time.Second
	defaultStickyCacheSize                         = 100

	envTaskQueue      = "TEMPORAL_TASK_QUEUE"
	envLambdaTaskRoot = "LAMBDA_TASK_ROOT"
	envConfigFile     = "TEMPORAL_CONFIG_FILE"
	defaultConfigFile = "temporal.toml"
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

// lambdaDefaultConfigFilePath returns the config file path to use in a Lambda environment. It
// respects TEMPORAL_CONFIG_FILE if set, otherwise defaults to temporal.toml in the Lambda code root
// (LAMBDA_TASK_ROOT). If LAMBDA_TASK_ROOT is not set, it falls back to temporal.toml in the current
// working directory.
func lambdaDefaultConfigFilePath(getenv func(string) string) string {
	if f := getenv(envConfigFile); f != "" {
		return f
	}
	root := getenv(envLambdaTaskRoot)
	if root == "" {
		root = "."
	}
	return path.Join(root, defaultConfigFile)
}
