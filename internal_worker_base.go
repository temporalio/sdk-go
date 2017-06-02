// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cadence

// All code in this file is private to the package.

import (
	"sync"
	"time"

	m "go.uber.org/cadence/.gen/go/cadence"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	retryPollOperationInitialInterval    = time.Millisecond
	retryPollOperationMaxInterval        = 1 * time.Second
	retryPollOperationExpirationInterval = backoff.NoInterval // We don't ever expire
)

var (
	pollOperationRetryPolicy = createPollRetryPolicy()
)

type (
	// resultHandler that returns result
	resultHandler func(result []byte, err error)

	// workflowEnvironment Represents the environment for workflow/decider.
	// Should only be used within the scope of workflow definition
	workflowEnvironment interface {
		asyncActivityClient
		workflowTimerClient
		SideEffect(f func() ([]byte, error), callback resultHandler)
		WorkflowInfo() *WorkflowInfo
		Complete(result []byte, err error)
		RegisterCancelHandler(handler func())
		RequestCancelWorkflow(domainName, workflowID, runID string) error
		ExecuteChildWorkflow(options workflowOptions, callback resultHandler, startedHandler func(r WorkflowExecution, e error)) error
		GetLogger() *zap.Logger
		RegisterSignalHandler(handler func(name string, input []byte))
	}

	// WorkflowDefinition wraps the code that can execute a workflow.
	workflowDefinition interface {
		Execute(env workflowEnvironment, input []byte)
		// Called for each non timed out startDecision event.
		// Executed after all history events since the previous decision are applied to workflowDefinition
		OnDecisionTaskStarted()
		StackTrace() string // Stack trace of all coroutines owned by the Dispatcher instance
		Close()
	}

	// WorkflowDefinitionFactory that returns a workflow definition for a specific
	// workflow type.
	workflowDefinitionFactory func(workflowType WorkflowType) (workflowDefinition, error)

	// baseWorkerOptions options to configure base worker.
	baseWorkerOptions struct {
		routineCount    int
		taskPoller      taskPoller
		workflowService m.TChanWorkflowService
		identity        string
		workerType      string
	}

	// baseWorker that wraps worker activities.
	baseWorker struct {
		options         baseWorkerOptions
		isWorkerStarted bool
		shutdownCh      chan struct{}              // Channel used to shut down the go routines.
		shutdownWG      sync.WaitGroup             // The WaitGroup for shutting down existing routines.
		rateLimiter     common.TokenBucket         // Poll rate limiter
		retrier         *backoff.ConcurrentRetrier // Service errors back off retrier
		logger          *zap.Logger
	}
)

func createPollRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialRetryPolicy(retryPollOperationInitialInterval)
	policy.SetMaximumInterval(retryPollOperationMaxInterval)
	policy.SetExpirationInterval(retryPollOperationExpirationInterval)
	return policy
}

func newBaseWorker(options baseWorkerOptions, logger *zap.Logger) *baseWorker {
	return &baseWorker{
		options:     options,
		shutdownCh:  make(chan struct{}),
		rateLimiter: common.NewTokenBucket(1000, common.NewRealTimeSource()),
		retrier:     backoff.NewConcurrentRetrier(pollOperationRetryPolicy),
		logger:      logger.With(zapcore.Field{Key: tagWorkerType, Type: zapcore.StringType, String: options.workerType}),
	}
}

// Start starts a fixed set of routines to do the work.
func (bw *baseWorker) Start() {
	if bw.isWorkerStarted {
		return
	}
	// Add the total number of routines to the wait group
	bw.shutdownWG.Add(bw.options.routineCount)

	// Launch the routines to do work
	for i := 0; i < bw.options.routineCount; i++ {
		go bw.execute(i)
	}

	bw.isWorkerStarted = true
	bw.logger.Info("Started Worker", zap.Int("RoutineCount", bw.options.routineCount))
}

// Shutdown is a blocking call and cleans up all the resources assosciated with worker.
func (bw *baseWorker) Stop() {
	if !bw.isWorkerStarted {
		return
	}
	close(bw.shutdownCh)

	// TODO: The poll is longer than the 10 seconds, we probably need some way to hard terminate the
	// poll routines as well.

	if success := awaitWaitGroup(&bw.shutdownWG, 10*time.Second); !success {
		bw.logger.Info("Worker timed out on waiting for shutdown.")
	}
}

// execute handler wraps call to process a task.
func (bw *baseWorker) execute(routineID int) {
	for {
		// Check if we have to backoff.
		// TODO: Check if this is needed concurrent retires (or) per connection retrier.
		bw.retrier.Throttle()

		// Check if we are rate limited
		if !bw.rateLimiter.Consume(1, time.Millisecond) {
			continue
		}

		err := bw.options.taskPoller.PollAndProcessSingleTask()
		if err == nil {
			bw.retrier.Succeeded()
		} else if isClientSideError(err) {
			bw.logger.Info("Poll and processing failed with client side error", zap.Int(tagRoutineID, routineID), zap.Error(err))
			// This doesn't count against server failures.
		} else {
			bw.logger.Info("Poll and processing task failed with error", zap.Int(tagRoutineID, routineID), zap.Error(err))
			bw.retrier.Failed()
		}

		select {
		// Shutdown the Routine.
		case <-bw.shutdownCh:
			bw.logger.Info("Worker shutting down.", zap.Int(tagRoutineID, routineID))
			bw.shutdownWG.Done()
			return

		// We have work to do.
		default:
		}
	}
}
