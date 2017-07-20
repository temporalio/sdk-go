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
		GetVersion(changeID string, minSupported, maxSupported Version) Version
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
		pollerCount       int
		maxConcurrentTask int
		maxTaskRps        float32
		taskWorker        taskPoller
		workflowService   m.TChanWorkflowService
		identity          string
		workerType        string
	}

	// baseWorker that wraps worker activities.
	baseWorker struct {
		options         baseWorkerOptions
		isWorkerStarted bool
		shutdownCh      chan struct{}              // Channel used to shut down the go routines.
		shutdownWG      sync.WaitGroup             // The WaitGroup for shutting down existing routines.
		pollRateLimiter common.TokenBucket         // Poll rate limiter
		taskRateLimiter common.TokenBucket         // Task rate limiter
		retrier         *backoff.ConcurrentRetrier // Service errors back off retrier
		logger          *zap.Logger

		pollerRequestCh chan struct{}
		taskQueueCh     chan interface{}
	}
)

func createPollRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialRetryPolicy(retryPollOperationInitialInterval)
	policy.SetMaximumInterval(retryPollOperationMaxInterval)
	policy.SetExpirationInterval(retryPollOperationExpirationInterval)
	return policy
}

func newBaseWorker(options baseWorkerOptions, logger *zap.Logger) *baseWorker {
	maxRpsInt := int(options.maxTaskRps)
	if maxRpsInt < 1 {
		// TokenBucket implementation does not support RPS < 1. Will need to update TokenBucket
		maxRpsInt = 1
	}
	return &baseWorker{
		options:         options,
		shutdownCh:      make(chan struct{}),
		pollRateLimiter: common.NewTokenBucket(1000, common.NewRealTimeSource()),
		taskRateLimiter: common.NewTokenBucket(maxRpsInt, common.NewRealTimeSource()),
		retrier:         backoff.NewConcurrentRetrier(pollOperationRetryPolicy),
		logger:          logger.With(zapcore.Field{Key: tagWorkerType, Type: zapcore.StringType, String: options.workerType}),

		pollerRequestCh: make(chan struct{}, options.maxConcurrentTask),
		taskQueueCh:     make(chan interface{}), // no buffer, so poller only able to poll new task after previous is dispatched.
	}
}

// Start starts a fixed set of routines to do the work.
func (bw *baseWorker) Start() {
	if bw.isWorkerStarted {
		return
	}

	for i := 0; i < bw.options.pollerCount; i++ {
		bw.shutdownWG.Add(1)
		go bw.runPoller()
	}
	bw.shutdownWG.Add(1)
	go bw.runTaskDispatcher()

	bw.isWorkerStarted = true
	traceLog(func() {
		bw.logger.Info("Started Worker",
			zap.Int("PollerCount", bw.options.pollerCount),
			zap.Int("MaxConcurrentTask", bw.options.maxConcurrentTask),
			zap.Float32("MaxTaskRps", bw.options.maxTaskRps),
		)
	})
}

func (bw *baseWorker) isShutdown() bool {
	select {
	case <-bw.shutdownCh:
		return true
	default:
		return false
	}
}

func (bw *baseWorker) runPoller() {
	defer bw.shutdownWG.Done()

	for {
		select {
		case <-bw.shutdownCh:
			return
		case <-bw.pollerRequestCh:
			ch := make(chan struct{})
			go func(ch chan struct{}) {
				bw.pollTask()
				close(ch)
			}(ch)

			// block until previous poll completed or return immediately when shutdown
			select {
			case <-bw.shutdownCh:
				return
			case <-ch:
			}
		}
	}
}

func (bw *baseWorker) runTaskDispatcher() {
	defer bw.shutdownWG.Done()

	for i := 0; i < bw.options.maxConcurrentTask; i++ {
		bw.pollerRequestCh <- struct{}{}
	}

	for {
		// wait for new task or shutdown
		select {
		case <-bw.shutdownCh:
			return
		case task := <-bw.taskQueueCh:
			// block until taskRateLimiter satisfied
			for !bw.taskRateLimiter.Consume(1, time.Millisecond*100) {
				if bw.isShutdown() {
					return
				}
			}
			go bw.processTask(task)
		}
	}
}

func (bw *baseWorker) pollTask() {
	var err error
	var task interface{}
	bw.retrier.Throttle()
	if bw.pollRateLimiter.Consume(1, time.Millisecond*100) {
		task, err = bw.options.taskWorker.PollTask()
		if err != nil {
			bw.retrier.Failed()
			if enableVerboseLogging {
				bw.logger.Debug("Failed to poll for task.", zap.Error(err))
			}
		} else {
			bw.retrier.Succeeded()
		}
	} else {
		// if rate limiter fails, we need to back off.
		bw.retrier.Failed()
	}

	if task != nil {
		bw.taskQueueCh <- task
	} else {
		bw.pollerRequestCh <- struct{}{} // poll failed, trigger a new pool
	}
}

func (bw *baseWorker) processTask(task interface{}) {
	err := bw.options.taskWorker.ProcessTask(task)
	if err != nil {
		if isClientSideError(err) {
			bw.logger.Info("Task processing failed with client side error", zap.Error(err))
		} else {
			bw.logger.Info("Task processing failed with error", zap.Error(err))
		}
	}

	bw.pollerRequestCh <- struct{}{}
}

func (bw *baseWorker) Run() {
	bw.Start()
	d := <-getKillSignal()
	traceLog(func() {
		bw.logger.Info("Worker has been killed", zap.String("Signal", d.String()))
	})
	bw.Stop()
}

// Shutdown is a blocking call and cleans up all the resources assosciated with worker.
func (bw *baseWorker) Stop() {
	if !bw.isWorkerStarted {
		return
	}
	close(bw.shutdownCh)

	// TODO: The poll is longer than wait time, we need some way to hard terminate the
	// poll routines.

	if success := awaitWaitGroup(&bw.shutdownWG, 2*time.Second); !success {
		traceLog(func() {
			bw.logger.Info("Worker timed out on waiting for shutdown.")
		})
	}
}
