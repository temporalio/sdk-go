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
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/uber/tchannel-go"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
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

// sets the rpc timeout for a tchannel context
func tchanTimeout(timeout time.Duration) func(builder *tchannel.ContextBuilder) {
	return func(b *tchannel.ContextBuilder) {
		b.SetTimeout(timeout)
	}
}

// sets the retry option for a tchannel context
func tchanRetryOption(retryOpt *tchannel.RetryOptions) func(builder *tchannel.ContextBuilder) {
	return func(b *tchannel.ContextBuilder) {
		b.SetRetryOptions(retryOpt)
	}
}

// newTChannelContext - Get a tchannel context
func newTChannelContext(options ...func(builder *tchannel.ContextBuilder)) (tchannel.ContextWithHeaders, context.CancelFunc) {
	builder := tchannel.NewContextBuilder(defaultRPCTimeout)
	builder.SetRetryOptions(retryDefaultOptions)
	builder.AddHeader(versionHeaderName, LibraryVersion)
	for _, opt := range options {
		opt(builder)
	}
	return builder.Build()
}

// GetWorkerIdentity gets a default identity for the worker.
func getWorkerIdentity(tasklistName string) string {
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "UnKnown"
	}
	return fmt.Sprintf("%d@%s@%s", os.Getpid(), hostName, tasklistName)
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
	var details []byte
	if wErr, ok := err.(ErrorWithDetails); ok {
		wErr.Details(&details)
		return wErr.Reason(), details
	}
	if wErr, ok := err.(CanceledError); ok {
		wErr.Details(&details)
		return "canceled", details
	}
	if wErr, ok := err.(PanicError); ok {
		return err.Error(), []byte(wErr.StackTrace())
	}

	return err.Error(), []byte("")
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
