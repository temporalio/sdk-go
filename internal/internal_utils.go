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
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pborman/uuid"
	"github.com/uber-go/tally"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/metrics"
	"go.uber.org/yarpc"
)

const (
	// libraryVersionHeaderName refers to the name of the
	// tchannel / http header that contains the client
	// library version
	libraryVersionHeaderName = "cadence-client-library-version"

	// featureVersionHeaderName refers to the name of the
	// tchannel / http header that contains the client
	// feature version
	featureVersionHeaderName = "cadence-client-feature-version"

	// clientImplHeaderName refers to the name of the
	// header that contains the client implementation
	clientImplHeaderName  = "cadence-client-name"
	clientImplHeaderValue = "uber-go"

	// defaultRPCTimeout is the default tchannel rpc call timeout
	defaultRPCTimeout = 10 * time.Second
	//minRPCTimeout is minimum rpc call timeout allowed
	minRPCTimeout = 1 * time.Second
	//maxRPCTimeout is maximum rpc call timeout allowed
	maxRPCTimeout = 20 * time.Second
)

var (
	// call header to cadence server
	yarpcCallOptions = []yarpc.CallOption{
		yarpc.WithHeader(libraryVersionHeaderName, LibraryVersion),
		yarpc.WithHeader(featureVersionHeaderName, FeatureVersion),
		yarpc.WithHeader(clientImplHeaderName, clientImplHeaderValue),
	}
)

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
	rpcTimeout := defaultRPCTimeout
	if ctx != nil {
		// Set rpc timeout less than context timeout to allow for retries when call gets lost
		now := time.Now()
		if expiration, ok := ctx.Deadline(); ok && expiration.After(now) {
			rpcTimeout = expiration.Sub(now) / 2
			// Make sure to not set rpc timeout lower than minRPCTimeout
			if rpcTimeout < minRPCTimeout {
				rpcTimeout = minRPCTimeout
			} else if rpcTimeout > maxRPCTimeout {
				rpcTimeout = maxRPCTimeout
			}
		}
	}
	builder := &contextBuilder{Timeout: rpcTimeout}
	if ctx != nil {
		builder.ParentContext = ctx
	}
	for _, opt := range options {
		opt(builder)
	}
	ctx, cancelFn := builder.Build()

	return ctx, cancelFn, yarpcCallOptions
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

// getErrorDetails gets reason and details.
func getErrorDetails(err error, dataConverter encoded.DataConverter) (string, []byte) {
	switch err := err.(type) {
	case *CustomError:
		var data []byte
		var err0 error
		switch details := err.details.(type) {
		case ErrorDetailsValues:
			data, err0 = encodeArgs(dataConverter, details)
		case *EncodedValues:
			data = details.values
		default:
			panic("unknown error type")
		}
		if err0 != nil {
			panic(err0)
		}
		return err.Reason(), data
	case *CanceledError:
		var data []byte
		var err0 error
		switch details := err.details.(type) {
		case ErrorDetailsValues:
			data, err0 = encodeArgs(dataConverter, details)
		case *EncodedValues:
			data = details.values
		default:
			panic("unknown error type")
		}
		if err0 != nil {
			panic(err0)
		}
		return errReasonCanceled, data
	case *PanicError:
		data, err0 := encodeArgs(dataConverter, []interface{}{err.Error(), err.StackTrace()})
		if err0 != nil {
			panic(err0)
		}
		return errReasonPanic, data
	case *TimeoutError:
		var data []byte
		var err0 error
		switch details := err.details.(type) {
		case ErrorDetailsValues:
			data, err0 = encodeArgs(dataConverter, details)
		case *EncodedValues:
			data = details.values
		default:
			panic("unknown error type")
		}
		if err0 != nil {
			panic(err0)
		}
		return fmt.Sprintf("%v %v", errReasonTimeout, err.timeoutType), data
	default:
		// will be convert to GenericError when receiving from server.
		return errReasonGeneric, []byte(err.Error())
	}
}

// constructError construct error from reason and details sending down from server.
func constructError(reason string, details []byte, dataConverter encoded.DataConverter) error {
	if strings.HasPrefix(reason, errReasonTimeout) {
		timeoutType := getTimeoutTypeFromErrReason(reason)
		details := newEncodedValues(details, dataConverter)
		return NewTimeoutError(timeoutType, details)
	}

	switch reason {
	case errReasonPanic:
		// panic error
		var msg, st string
		details := newEncodedValues(details, dataConverter)
		details.Get(&msg, &st)
		return newPanicError(msg, st)
	case errReasonGeneric:
		// errors created other than using NewCustomError() API.
		return &GenericError{err: string(details)}
	case errReasonCanceled:
		details := newEncodedValues(details, dataConverter)
		return NewCanceledError(details)
	default:
		details := newEncodedValues(details, dataConverter)
		err := NewCustomError(reason, details)
		return err
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

// getMetricsScopeForActivity return properly tagged tally scope for activity
func getMetricsScopeForActivity(ts *metrics.TaggedScope, workflowType, activityType string) tally.Scope {
	return ts.GetTaggedScope(tagWorkflowType, workflowType, tagActivityType, activityType)
}

// getMetricsScopeForLocalActivity return properly tagged tally scope for local activity
func getMetricsScopeForLocalActivity(ts *metrics.TaggedScope, workflowType, localActivityType string) tally.Scope {
	return ts.GetTaggedScope(tagWorkflowType, workflowType, tagLocalActivityType, localActivityType)
}

func getTimeoutTypeFromErrReason(reason string) s.TimeoutType {
	timeoutTypeStr := reason[strings.Index(reason, " ")+1:]
	var timeoutType s.TimeoutType
	if err := timeoutType.UnmarshalText([]byte(timeoutTypeStr)); err != nil {
		// this should never happen
		panic(err)
	}
	return timeoutType
}
