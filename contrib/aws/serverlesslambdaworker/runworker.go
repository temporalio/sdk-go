package serverlesslambdaworker

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
// the client. RunWorker does not return under normal operation. On fatal configuration error, it
// logs to stderr and calls os.Exit(1).
func RunWorker(configure func(ctx *ConfigureWorkerContext) error) {
	deps := defaultDeps()
	if err := runWorkerInternal(configure, deps); err != nil {
		fmt.Fprintf(os.Stderr, "serverlesslambdaworker: fatal: %v\n", err)
		deps.exit(1)
	}
}

// runWorkerInternal contains the core logic with injected dependencies for testability. It returns
// an error instead of calling os.Exit.
func runWorkerInternal(configure func(ctx *ConfigureWorkerContext) error, deps workerDeps) error {
	configCtx := &ConfigureWorkerContext{}
	if err := configure(configCtx); err != nil {
		return fmt.Errorf("configure callback failed: %w", err)
	}

	clientOpts, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("loading client config: %w", err)
	}
	applyLambdaClientDefaults(&clientOpts)
	if configCtx.mutateClientOpts != nil {
		if err := configCtx.mutateClientOpts(&clientOpts); err != nil {
			return fmt.Errorf("mutating client options: %w", err)
		}
	}

	taskQueue, err := resolveTaskQueue(configCtx.taskQueue, deps.getenv)
	if err != nil {
		return err
	}

	var workerOpts worker.Options
	applyLambdaWorkerDefaults(&workerOpts)
	if configCtx.mutateWorkerOpts != nil {
		if err := configCtx.mutateWorkerOpts(&workerOpts); err != nil {
			return fmt.Errorf("mutating worker options: %w", err)
		}
	}
	deps.setCacheSize(defaultStickyCacheSize)

	stopTimeout := workerOpts.WorkerStopTimeout

	handler := func(invocationCtx context.Context) error {
		// Build per-invocation client options with identity from this invocation's
		// Lambda context. A shallow copy is sufficient since only Identity is modified.
		invocationOpts := clientOpts
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

		w := deps.newWorker(c, taskQueue, workerOpts)
		configCtx.replayRegistrations(w)

		if err := w.Start(); err != nil {
			return fmt.Errorf("starting worker: %w", err)
		}
		defer w.Stop()

		deadline, ok := invocationCtx.Deadline()
		if !ok {
			<-invocationCtx.Done()
		} else {
			shutdownAt := deadline.Add(-stopTimeout)
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

		for _, fn := range configCtx.shutdownFuncs {
			if err := fn(invocationCtx); err != nil {
				fmt.Fprintf(os.Stderr, "serverlesslambdaworker: shutdown hook error: %v\n", err)
			}
		}
		return nil
	}

	deps.startLambda(handler)
	return nil
}
