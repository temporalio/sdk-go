package lambdaworker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/worker"
)

// workerDeps captures external dependencies for testability.
type workerDeps struct {
	dial             func(client.Options) (client.Client, error)
	newWorker        func(client.Client, string, worker.Options) worker.Worker
	startLambda      func(handler any, options ...lambda.Option)
	loadConfig       func() (client.Options, error)
	getenv           func(string) string
	setCacheSize     func(int)
	exit             func(int)
	extractLambdaCtx func(context.Context) (requestID string, functionARN string, ok bool)
}

func defaultDeps() workerDeps {
	return workerDeps{
		dial: func(opts client.Options) (client.Client, error) { return client.Dial(opts) },
		newWorker: func(c client.Client, tq string, opts worker.Options) worker.Worker {
			return worker.New(c, tq, opts)
		},
		startLambda: lambda.StartWithOptions,
		loadConfig: func() (client.Options, error) {
			return envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
				ConfigFilePath: lambdaDefaultConfigFilePath(os.Getenv),
			})
		},
		getenv:       os.Getenv,
		setCacheSize: worker.SetStickyWorkflowCacheSize,
		exit:         os.Exit,
		extractLambdaCtx: func(ctx context.Context) (string, string, bool) {
			lc, ok := lambdacontext.FromContext(ctx)
			if !ok {
				return "", "", false
			}
			return lc.AwsRequestID, lc.InvokedFunctionArn, true
		},
	}
}

// RunWorker starts a Temporal worker inside an AWS Lambda execution environment. It calls the
// configure callback to collect registrations and option overrides, then delegates to the Lambda
// runtime. On each invocation, it dials the Temporal server, starts a worker, polls for tasks
// until the invocation deadline approaches, and then gracefully shuts down the worker and closes
// the client. RunWorker does not return under normal operation.
//
// You must configure a task queue for the worker to listen on, either via
// [Options.TaskQueue] or the TEMPORAL_TASK_QUEUE environment variable.
// You must also register one or more Workflows, Activities, or Nexus Services by using the
// registration methods provided by [Options].
//
// On fatal configuration error, it logs to stderr and calls os.Exit(1).
func RunWorker(configure func(ctx *Options) error) {
	deps := defaultDeps()
	if err := runWorkerInternal(configure, deps); err != nil {
		fmt.Fprintf(os.Stderr, "lambdaworker: fatal: %v\n", err)
		deps.exit(1)
	}
}

// runWorkerInternal contains the core logic with injected dependencies for testability. It returns
// an error instead of calling os.Exit.
func runWorkerInternal(configure func(ctx *Options) error, deps workerDeps) error {
	clientOpts, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("loading client config: %w", err)
	}
	applyLambdaClientDefaults(&clientOpts)

	var workerOpts worker.Options
	applyLambdaWorkerDefaults(&workerOpts)

	deps.setCacheSize(defaultStickyCacheSize)

	configCtx := &Options{
		ClientOptions:          clientOpts,
		WorkerOptions:          workerOpts,
		ShutdownDeadlineBuffer: workerOpts.WorkerStopTimeout + defaultShutdownHookBuffer,
	}
	if tq := deps.getenv(envTaskQueue); tq != "" {
		configCtx.TaskQueue = tq
	}

	if err := configure(configCtx); err != nil {
		return fmt.Errorf("configure callback failed: %w", err)
	}

	if configCtx.TaskQueue == "" {
		return fmt.Errorf(
			"task queue not configured: set Options.TaskQueue or the %s environment variable",
			envTaskQueue,
		)
	}

	logger := configCtx.ClientOptions.Logger

	handler := func(invocationCtx context.Context) error {
		deadline, ok := invocationCtx.Deadline()
		if ok {
			workTime := time.Until(deadline) - configCtx.ShutdownDeadlineBuffer
			if workTime <= time.Second {
				return fmt.Errorf(
					"Lambda timeout leaves almost no time for work "+
						"(workTime %v, shutdownBuffer %v); increase the "+
						"function timeout or decrease the shutdown deadline "+
						"buffer",
					workTime, configCtx.ShutdownDeadlineBuffer,
				)
			} else if workTime < 5*time.Second {
				if logger != nil {
					logger.Warn(
						"Lambda timeout leaves less than 5s for work after "+
							"shutdown buffer; consider increasing the function "+
							"timeout or decreasing the shutdown deadline buffer",
						"workTime", workTime,
						"shutdownBuffer", configCtx.ShutdownDeadlineBuffer,
					)
				}
			}
		}

		// Build per-invocation client options with identity from this invocation's Lambda context.
		// A shallow copy is sufficient since only Identity is modified.
		invocationOpts := configCtx.ClientOptions
		if invocationOpts.Identity == "" {
			requestID, functionARN, ok := deps.extractLambdaCtx(invocationCtx)
			if ok {
				invocationOpts.Identity = buildLambdaIdentity(requestID, functionARN)
			}
		}

		c, err := deps.dial(invocationOpts)
		if err != nil {
			return fmt.Errorf("dialing Temporal server: %w", err)
		}
		defer c.Close()

		w := deps.newWorker(c, configCtx.TaskQueue, configCtx.WorkerOptions)
		configCtx.replayRegistrations(w)

		if err := w.Start(); err != nil {
			return fmt.Errorf("starting worker: %w", err)
		}

		if !ok {
			<-invocationCtx.Done()
		} else {
			shutdownAt := deadline.Add(-configCtx.ShutdownDeadlineBuffer)
			waitDuration := time.Until(shutdownAt)
			if waitDuration > 0 {
				timer := time.NewTimer(waitDuration)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-invocationCtx.Done():
				}
			}
		}

		// Stop the worker before running shutdown hooks so that hooks (e.g. OTLP telemetry flushes)
		// see all spans/metrics emitted during the drain phase.
		w.Stop()

		// Use context.Background because invocationCtx may already be cancelled. No timeout is
		// needed — Lambda hard-kills the process at the deadline regardless.
		for _, fn := range configCtx.shutdownFuncs {
			if err := fn(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr,
					"lambdaworker: shutdown hook error: %v\n", err)
			}
		}
		return nil
	}

	deps.startLambda(handler)
	return nil
}
