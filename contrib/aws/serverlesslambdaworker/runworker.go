package serverlesslambdaworker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/worker"
)

// workerDeps captures external dependencies for testability.
type workerDeps struct {
	dial         func(client.Options) (client.Client, error)
	newWorker    func(client.Client, string, worker.Options) worker.Worker
	startLambda  func(handler interface{}, options ...lambda.Option)
	loadConfig   func() (client.Options, error)
	getenv       func(string) string
	setCacheSize func(int)
	exit         func(int)
}

func defaultDeps() workerDeps {
	return workerDeps{
		dial: func(opts client.Options) (client.Client, error) { return client.Dial(opts) },
		newWorker: func(c client.Client, tq string, opts worker.Options) worker.Worker {
			return worker.New(c, tq, opts)
		},
		startLambda:  lambda.StartWithOptions,
		loadConfig:   envconfig.LoadDefaultClientOptions,
		getenv:       os.Getenv,
		setCacheSize: worker.SetStickyWorkflowCacheSize,
		exit:         os.Exit,
	}
}

// RunWorker starts a Temporal worker inside an AWS Lambda execution environment. It calls the
// configure callback to collect registrations and option overrides, creates a Temporal client and
// worker with Lambda-appropriate defaults, and delegates to the Lambda runtime. RunWorker does not
// return under normal operation. On fatal initialization error, it logs to stderr and calls
// os.Exit(1).
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
	ctx := &ConfigureWorkerContext{}
	if err := configure(ctx); err != nil {
		return fmt.Errorf("configure callback failed: %w", err)
	}

	clientOpts, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("loading client config: %w", err)
	}
	applyLambdaClientDefaults(&clientOpts, deps.getenv)
	if ctx.mutateClientOpts != nil {
		ctx.mutateClientOpts(&clientOpts)
	}

	temporalClient, err := deps.dial(clientOpts)
	if err != nil {
		return fmt.Errorf("dialing Temporal server: %w", err)
	}
	defer temporalClient.Close()

	taskQueue, err := resolveTaskQueue(ctx.taskQueue, deps.getenv)
	if err != nil {
		return err
	}

	var workerOpts worker.Options
	applyLambdaWorkerDefaults(&workerOpts)
	if ctx.mutateWorkerOpts != nil {
		ctx.mutateWorkerOpts(&workerOpts)
	}

	deps.setCacheSize(defaultStickyCacheSize)
	w := deps.newWorker(temporalClient, taskQueue, workerOpts)
	ctx.replayRegistrations(w)

	if err := w.Start(); err != nil {
		return fmt.Errorf("starting worker: %w", err)
	}
	defer w.Stop()

	stopTimeout := workerOpts.WorkerStopTimeout
	handler := func(invocationCtx context.Context) error {
		deadline, ok := invocationCtx.Deadline()
		if !ok {
			// No deadline: wait for context cancellation.
			<-invocationCtx.Done()
		} else {
			// Wait until (deadline - stopTimeout) to give the worker time to drain.
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
		return nil
	}

	deps.startLambda(handler,
		lambda.WithEnableSIGTERM(func() {
			w.Stop()
			temporalClient.Close()
		}),
	)

	return nil
}
