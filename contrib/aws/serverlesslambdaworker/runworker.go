package serverlesslambdaworker

import (
	"context"
	"fmt"
	"os"
	"sync"
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
	startLambda      func(handler interface{}, options ...lambda.Option)
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
		startLambda:  lambda.StartWithOptions,
		loadConfig:   envconfig.LoadDefaultClientOptions,
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
// runtime. On the first invocation, it dials the Temporal server and starts the worker using
// identity information from the Lambda invocation context. RunWorker does not return under normal
// operation. On fatal initialization error, it logs to stderr and calls os.Exit(1).
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
		configCtx.mutateClientOpts(&clientOpts)
	}

	taskQueue, err := resolveTaskQueue(configCtx.taskQueue, deps.getenv)
	if err != nil {
		return err
	}

	var workerOpts worker.Options
	applyLambdaWorkerDefaults(&workerOpts)
	if configCtx.mutateWorkerOpts != nil {
		configCtx.mutateWorkerOpts(&workerOpts)
	}
	deps.setCacheSize(defaultStickyCacheSize)

	var (
		temporalClient client.Client
		w              worker.Worker
		initOnce       sync.Once
		initErr        error
	)

	cleanup := func() {
		if w != nil {
			w.Stop()
		}
		if temporalClient != nil {
			temporalClient.Close()
		}
	}
	defer cleanup()

	deferredInit := func(invocationCtx context.Context) error {
		initOnce.Do(func() {
			// Set identity from Lambda context if not already set by user or envconfig.
			// Identity is derived from the first invocation's request ID and function ARN.
			if clientOpts.Identity == "" {
				requestID, functionARN, ok := deps.extractLambdaCtx(invocationCtx)
				if ok {
					clientOpts.Identity = buildLambdaIdentity(requestID, functionARN)
				}
			}

			c, err := deps.dial(clientOpts)
			if err != nil {
				initErr = fmt.Errorf("dialing Temporal server: %w", err)
				return
			}

			wk := deps.newWorker(c, taskQueue, workerOpts)
			configCtx.replayRegistrations(wk)

			if err = wk.Start(); err != nil {
				c.Close()
				initErr = fmt.Errorf("starting worker: %w", err)
				return
			}

			temporalClient = c
			w = wk
		})
		return initErr
	}

	stopTimeout := workerOpts.WorkerStopTimeout

	handler := func(invocationCtx context.Context) error {
		if err := deferredInit(invocationCtx); err != nil {
			return err
		}

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
		return nil
	}

	deps.startLambda(handler, lambda.WithEnableSIGTERM(cleanup))
	return nil
}
