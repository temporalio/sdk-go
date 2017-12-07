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

package internal

// All code in this file is private to the package.

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pborman/uuid"
	"github.com/uber/tchannel-go"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/yarpc"
	"golang.org/x/net/context"
)

// versionHeaderName refers to the name of the
// tchannel / http header that contains the client
// library version
const versionHeaderName = "cadence-client-version"

// defaultRPCTimeout is the default tchannel rpc call timeout
const defaultRPCTimeout = 10 * time.Second

// retryNeverOptions - Never retry the connection
var retryNeverOptions = &tchannel.RetryOptions{
	RetryOn: tchannel.RetryNever,
}

// retryDefaultOptions - retry with default options.
var retryDefaultOptions = &tchannel.RetryOptions{
	RetryOn: tchannel.RetryDefault,
}

// ContextBuilder stores all Channel-specific parameters that will
// be stored inside of a context.
type contextBuilder struct {
	// If Timeout is zero, Build will default to defaultTimeout.
	Timeout time.Duration

	// ParentContext to build the new context from. If empty, context.Background() is used.
	// The new (child) context inherits a number of properties from the parent context:
	//   - context fields, accessible via `ctx.Value(key)`
	ParentContext context.Context
}

func (cb *contextBuilder) Build() (context.Context, context.CancelFunc) {
	parent := cb.ParentContext
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, cb.Timeout)
}

// sets the rpc timeout for a context
func chanTimeout(timeout time.Duration) func(builder *contextBuilder) {
	return func(b *contextBuilder) {
		b.Timeout = timeout
	}
}

// newChannelContext - Get a rpc channel context
func newChannelContext(ctx context.Context, options ...func(builder *contextBuilder)) (context.Context, context.CancelFunc, []yarpc.CallOption) {
	builder := &contextBuilder{Timeout: defaultRPCTimeout}
	if ctx != nil {
		builder.ParentContext = ctx
	}
	for _, opt := range options {
		opt(builder)
	}
	ctx, cancelFn := builder.Build()

	callOptions := []yarpc.CallOption{
		yarpc.WithHeader(versionHeaderName, LibraryVersion)}

	return ctx, cancelFn, callOptions
}

// GetWorkerIdentity gets a default identity for the worker.
func getWorkerIdentity(tasklistName string) string {
	return fmt.Sprintf("%d@%s@%s", os.Getpid(), getHostName(), tasklistName)
}

func getHostName() string {
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "UnKnown"
	}
	return hostName
}

// worker uuid per process
var workerUUID = uuid.New()

func getWorkerTaskList() string {
	// includes hostname for debuggability, workerUUID guarantees the uniqueness
	return fmt.Sprintf("%s:%s", getHostName(), workerUUID)
}

func flowActivityTypeFrom(v s.ActivityType) ActivityType {
	return ActivityType{Name: v.GetName()}
}

// ActivityTypePtr makes a copy and returns the pointer to a ActivityType.
func activityTypePtr(v ActivityType) *s.ActivityType {
	return &s.ActivityType{Name: common.StringPtr(v.Name)}
}

func flowWorkflowTypeFrom(v s.WorkflowType) WorkflowType {
	return WorkflowType{Name: v.GetName()}
}

// WorkflowTypePtr makes a copy and returns the pointer to a WorkflowType.
func workflowTypePtr(t WorkflowType) *s.WorkflowType {
	return &s.WorkflowType{Name: common.StringPtr(t.Name)}
}

// workflowExecutionPtr makes a copy and returns the pointer to a WorkflowExecution.
func workflowExecutionPtr(t WorkflowExecution) *s.WorkflowExecution {
	return &s.WorkflowExecution{
		WorkflowId: common.StringPtr(t.ID),
		RunId:      common.StringPtr(t.RunID),
	}
}

// getErrorDetails gets reason and details.
func getErrorDetails(err error) (string, []byte) {
	switch err := err.(type) {
	case *CustomError:
		return err.Reason(), err.details
	case *CanceledError:
		return errReasonCanceled, err.details
	case *PanicError:
		data, gobErr := getHostEnvironment().encodeArgs([]interface{}{err.Error(), err.StackTrace()})
		if gobErr != nil {
			panic(gobErr)
		}
		return errReasonPanic, data
	default:
		// will be convert to GenericError when receiving from server.
		return errReasonGeneric, []byte(err.Error())
	}
}

// constructError construct error from reason and details sending down from server.
func constructError(reason string, details []byte) error {
	switch reason {
	case errReasonPanic:
		// panic error
		var msg, st string
		details := EncodedValues(details)
		details.Get(&msg, &st)
		return newPanicError(msg, st)
	case errReasonGeneric:
		// errors created other than using NewCustomError() API.
		return &GenericError{err: string(details)}
	case errReasonCanceled:
		return NewCanceledError(details)
	default:
		return NewCustomError(reason, details)
	}
}

// AwaitWaitGroup calls Wait on the given wait
// Returns true if the Wait() call succeeded before the timeout
// Returns false if the Wait() did not return before the timeout
func awaitWaitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	doneC := make(chan struct{})

	go func() {
		wg.Wait()
		close(doneC)
	}()

	select {
	case <-doneC:
		return true
	case <-time.After(timeout):
		return false
	}
}

func getKillSignal() <-chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	return c
}
