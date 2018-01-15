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

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uber-go/tally"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/zap"
)

var (
	errActivityParamsBadRequest = errors.New("missing activity parameters through context, check ActivityOptions")
	errWorkflowOptionBadRequest = errors.New("missing workflow options through context, check WorkflowOptions")
)

type (

	// Channel must be used instead of native go channel by workflow code.
	// Use workflow.NewChannel(ctx) method to create Channel instance.
	Channel interface {
		// Receive blocks until it receives a value, and then assigns the received value to the provided pointer.
		// Returns false when Channel is closed.
		// Parameter valuePtr is a pointer to the expected data structure to be received. For example:
		//  var v string
		//  c.Receive(ctx, &v)
		Receive(ctx Context, valuePtr interface{}) (more bool)

		// ReceiveAsync try to receive from Channel without blocking. If there is data available from the Channel, it
		// assign the data to valuePtr and returns true. Otherwise, it returns false immediately.
		ReceiveAsync(valuePtr interface{}) (ok bool)

		// ReceiveAsyncWithMoreFlag is same as ReceiveAsync with extra return value more to indicate if there could be
		// more value from the Channel. The more is false when Channel is closed.
		ReceiveAsyncWithMoreFlag(valuePtr interface{}) (ok bool, more bool)

		// Send blocks until the data is sent.
		Send(ctx Context, v interface{})

		// SendAsync try to send without blocking. It returns true if the data was sent, otherwise it returns false.
		SendAsync(v interface{}) (ok bool)

		// Close close the Channel, and prohibit subsequent sends.
		Close()
	}

	// Selector must be used instead of native go select by workflow code.
	// Use workflow.NewSelector(ctx) method to create a Selector instance.
	Selector interface {
		AddReceive(c Channel, f func(c Channel, more bool)) Selector
		AddSend(c Channel, v interface{}, f func()) Selector
		AddFuture(future Future, f func(f Future)) Selector
		AddDefault(f func())
		Select(ctx Context)
	}

	// Future represents the result of an asynchronous computation.
	Future interface {
		// Get blocks until the future is ready. When ready it either returns non nil error or assigns result value to
		// the provided pointer.
		// Example:
		//  var v string
		//  if err := f.Get(ctx, &v); err != nil {
		//      return err
		//  }
		Get(ctx Context, valuePtr interface{}) error

		// When true Get is guaranteed to not block
		IsReady() bool
	}

	// Settable is used to set value or error on a future.
	// See more: workflow.NewFuture(ctx).
	Settable interface {
		Set(value interface{}, err error)
		SetValue(value interface{})
		SetError(err error)
		Chain(future Future) // Value (or error) of the future become the same of the chained one.
	}

	// ChildWorkflowFuture represents the result of a child workflow execution
	ChildWorkflowFuture interface {
		Future
		// GetChildWorkflowExecution returns a future that will be ready when child workflow execution started. You can
		// get the WorkflowExecution of the child workflow from the future. Then you can use Workflow ID and RunID of
		// child workflow to cancel or send signal to child workflow.
		//  childWorkflowFuture := workflow.ExecuteChildWorkflow(ctx, child, ...)
		//  var childWE WorkflowExecution
		//  if err := childWorkflowFuture.GetChildWorkflowExecution().Get(&childWE); err == nil {
		//      // child workflow started, you can use childWE to get the WorkflowID and RunID of child workflow
		//  }
		GetChildWorkflowExecution() Future
	}

	// WorkflowType identifies a workflow type.
	WorkflowType struct {
		Name string
	}

	// WorkflowExecution Details.
	WorkflowExecution struct {
		ID    string
		RunID string
	}

	// EncodedValue is type alias used to encapsulate/extract encoded result from workflow/activity.
	EncodedValue []byte

	// Version represents a change version. See GetVersion call.
	Version int

	// ChildWorkflowOptions stores all child workflow specific parameters that will be stored inside of a Context.
	ChildWorkflowOptions struct {
		// Domain of the child workflow.
		// Optional: the current workflow (parent)'s domain will be used if this is not provided.
		Domain string

		// WorkflowID of the child workflow to be scheduled.
		// Optional: an auto generated workflowID will be used if this is not provided.
		WorkflowID string

		// TaskList that the child workflow needs to be scheduled on.
		// Optional: the parent workflow task list will be used if this is not provided.
		TaskList string

		// ExecutionStartToCloseTimeout - The end to end timeout for the child workflow execution.
		// Mandatory: no default
		ExecutionStartToCloseTimeout time.Duration

		// TaskStartToCloseTimeout - The decision task timeout for the child workflow.
		// Optional: default is 10s if this is not provided (or if 0 is provided).
		TaskStartToCloseTimeout time.Duration

		// ChildPolicy defines the behavior of child workflow when parent workflow is terminated.
		// Optional: default to use ChildWorkflowPolicyTerminate if this is not provided
		ChildPolicy ChildWorkflowPolicy

		// WaitForCancellation - Whether to wait for cancelled child workflow to be ended (child workflow can be ended
		// as: completed/failed/timedout/terminated/canceled)
		// Optional: default false
		WaitForCancellation bool

		// WorkflowIDReusePolicy - Whether server allow reuse of workflow ID, can be useful
		// for dedup logic if set to WorkflowIdReusePolicyRejectDuplicate
		WorkflowIDReusePolicy WorkflowIDReusePolicy
	}

	// ChildWorkflowPolicy defines child workflow behavior when parent workflow is terminated.
	ChildWorkflowPolicy int32
)

const (
	// ChildWorkflowPolicyTerminate is policy that will terminate all child workflows when parent workflow is terminated.
	ChildWorkflowPolicyTerminate ChildWorkflowPolicy = 0
	// ChildWorkflowPolicyRequestCancel is policy that will send cancel request to all open child workflows when parent
	// workflow is terminated.
	ChildWorkflowPolicyRequestCancel ChildWorkflowPolicy = 1
	// ChildWorkflowPolicyAbandon is policy that will have no impact to child workflow execution when parent workflow is
	// terminated.
	ChildWorkflowPolicyAbandon ChildWorkflowPolicy = 2
)

// RegisterWorkflowOptions consists of options for registering a workflow
type RegisterWorkflowOptions struct {
	Name string
}

// RegisterWorkflow - registers a workflow function with the framework.
// A workflow takes a cadence context and input and returns a (result, error) or just error.
// Examples:
//	func sampleWorkflow(ctx workflow.Context, input []byte) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context, arg1 int, arg2 string) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context, arg1 int) (result string, err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
// This method calls panic if workflowFunc doesn't comply with the expected format.
func RegisterWorkflow(workflowFunc interface{}) {
	RegisterWorkflowWithOptions(workflowFunc, RegisterWorkflowOptions{})
}

// RegisterWorkflowWithOptions registers the workflow function with options
// The user can use options to provide an external name for the workflow or leave it empty if no
// external name is required. This can be used as
//  client.RegisterWorkflow(sampleWorkflow, RegisterWorkflowOptions{})
//  client.RegisterWorkflow(sampleWorkflow, RegisterWorkflowOptions{Name: "foo"})
// A workflow takes a cadence context and input and returns a (result, error) or just error.
// Examples:
//	func sampleWorkflow(ctx workflow.Context, input []byte) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context, arg1 int, arg2 string) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context) (result []byte, err error)
//	func sampleWorkflow(ctx workflow.Context, arg1 int) (result string, err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
// This method calls panic if workflowFunc doesn't comply with the expected format.
func RegisterWorkflowWithOptions(workflowFunc interface{}, opts RegisterWorkflowOptions) {
	thImpl := getHostEnvironment()
	err := thImpl.RegisterWorkflowWithOptions(workflowFunc, opts)
	if err != nil {
		panic(err)
	}
}

// NewChannel create new Channel instance
func NewChannel(ctx Context) Channel {
	state := getState(ctx)
	state.dispatcher.channelSequence++
	return NewNamedChannel(ctx, fmt.Sprintf("chan-%v", state.dispatcher.channelSequence))
}

// NewNamedChannel create new Channel instance with a given human readable name.
// Name appears in stack traces that are blocked on this channel.
func NewNamedChannel(ctx Context, name string) Channel {
	return &channelImpl{name: name}
}

// NewBufferedChannel create new buffered Channel instance
func NewBufferedChannel(ctx Context, size int) Channel {
	return &channelImpl{size: size}
}

// NewNamedBufferedChannel create new BufferedChannel instance with a given human readable name.
// Name appears in stack traces that are blocked on this Channel.
func NewNamedBufferedChannel(ctx Context, name string, size int) Channel {
	return &channelImpl{name: name, size: size}
}

// NewSelector creates a new Selector instance.
func NewSelector(ctx Context) Selector {
	state := getState(ctx)
	state.dispatcher.selectorSequence++
	return NewNamedSelector(ctx, fmt.Sprintf("selector-%v", state.dispatcher.selectorSequence))
}

// NewNamedSelector creates a new Selector instance with a given human readable name.
// Name appears in stack traces that are blocked on this Selector.
func NewNamedSelector(ctx Context, name string) Selector {
	return &selectorImpl{name: name}
}

// Go creates a new coroutine. It has similar semantic to goroutine in a context of the workflow.
func Go(ctx Context, f func(ctx Context)) {
	state := getState(ctx)
	state.dispatcher.newCoroutine(ctx, f)
}

// GoNamed creates a new coroutine with a given human readable name.
// It has similar semantic to goroutine in a context of the workflow.
// Name appears in stack traces that are blocked on this Channel.
func GoNamed(ctx Context, name string, f func(ctx Context)) {
	state := getState(ctx)
	state.dispatcher.newNamedCoroutine(ctx, name, f)
}

// NewFuture creates a new future as well as associated Settable that is used to set its value.
func NewFuture(ctx Context) (Future, Settable) {
	impl := &futureImpl{channel: NewChannel(ctx).(*channelImpl)}
	return impl, impl
}

// ExecuteActivity requests activity execution in the context of a workflow.
// Context can be used to pass the settings for this activity.
// For example: task list that this need to be routed, timeouts that need to be configured.
// Use ActivityOptions to pass down the options.
//  ao := ActivityOptions{
// 	    TaskList: "exampleTaskList",
// 	    ScheduleToStartTimeout: 10 * time.Second,
// 	    StartToCloseTimeout: 5 * time.Second,
// 	    ScheduleToCloseTimeout: 10 * time.Second,
// 	    HeartbeatTimeout: 0,
// 	}
//	ctx := WithActivityOptions(ctx, ao)
// Or to override a single option
//  ctx := WithTaskList(ctx, "exampleTaskList")
// Input activity is either an activity name (string) or a function representing an activity that is getting scheduled.
// Input args are the arguments that need to be passed to the scheduled activity.
//
// If the activity failed to complete then the future get error would indicate the failure, and it can be one of
// CustomError, TimeoutError, CanceledError, PanicError, GenericError.
// You can cancel the pending activity using context(workflow.WithCancel(ctx)) and that will fail the activity with
// error CanceledError.
//
// ExecuteActivity returns Future with activity result or failure.
func ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future {
	// Validate type and its arguments.
	future, settable := newDecodeFuture(ctx, activity)
	activityType, input, err := getValidatedActivityFunction(activity, args)
	if err != nil {
		settable.Set(nil, err)
		return future
	}
	// Validate context options.
	parameters := getActivityOptions(ctx)
	parameters, err = getValidatedActivityOptions(ctx)
	if err != nil {
		settable.Set(nil, err)
		return future
	}
	parameters.ActivityType = *activityType
	parameters.Input = input

	a := getWorkflowEnvironment(ctx).ExecuteActivity(*parameters, func(r []byte, e error) {
		settable.Set(r, e)
	})
	Go(ctx, func(ctx Context) {
		if ctxDone := ctx.Done(); ctxDone != nil {
			NewSelector(ctx).AddReceive(ctxDone, func(c Channel, more bool) {
				if ctx.Err() == ErrCanceled {
					getWorkflowEnvironment(ctx).RequestCancelActivity(a.activityID)
				}
			}).AddFuture(future, func(f Future) {
				// activity is done, no-op
			}).Select(ctx)
		}
	})
	return future
}

// ExecuteChildWorkflow requests child workflow execution in the context of a workflow.
// Context can be used to pass the settings for the child workflow.
// For example: task list that this child workflow should be routed, timeouts that need to be configured.
// Use ChildWorkflowOptions to pass down the options.
//  cwo := ChildWorkflowOptions{
// 	    ExecutionStartToCloseTimeout: 10 * time.Minute,
// 	    TaskStartToCloseTimeout: time.Minute,
// 	}
//  ctx := WithChildWorkflowOptions(ctx, cwo)
// Input childWorkflow is either a workflow name or a workflow function that is getting scheduled.
// Input args are the arguments that need to be passed to the child workflow function represented by childWorkflow.
// If the child workflow failed to complete then the future get error would indicate the failure and it can be one of
// CustomError, TimeoutError, CanceledError, GenericError.
// You can cancel the pending child workflow using context(workflow.WithCancel(ctx)) and that will fail the workflow with
// error CanceledError.
// ExecuteChildWorkflow returns ChildWorkflowFuture.
func ExecuteChildWorkflow(ctx Context, childWorkflow interface{}, args ...interface{}) ChildWorkflowFuture {
	mainFuture, mainSettable := newDecodeFuture(ctx, childWorkflow)
	executionFuture, executionSettable := NewFuture(ctx)
	result := childWorkflowFutureImpl{
		decodeFutureImpl: mainFuture.(*decodeFutureImpl),
		executionFuture:  executionFuture.(*futureImpl)}
	wfType, input, err := getValidatedWorkflowFunction(childWorkflow, args)
	if err != nil {
		mainSettable.Set(nil, err)
		return result
	}
	options, err := getValidatedWorkflowOptions(ctx)
	if err != nil {
		mainSettable.Set(nil, err)
		return result
	}

	options.input = input
	options.workflowType = wfType
	var childWorkflowExecution *WorkflowExecution
	getWorkflowEnvironment(ctx).ExecuteChildWorkflow(*options, func(r []byte, e error) {
		mainSettable.Set(r, e)
	}, func(r WorkflowExecution, e error) {
		if e == nil {
			childWorkflowExecution = &r
		}
		executionSettable.Set(r, e)
	})
	Go(ctx, func(ctx Context) {
		if ctxDone := ctx.Done(); ctxDone != nil {
			NewSelector(ctx).AddReceive(ctxDone, func(c Channel, more bool) {
				if ctx.Err() == ErrCanceled && childWorkflowExecution != nil {
					// child workflow started, and ctx cancelled
					getWorkflowEnvironment(ctx).RequestCancelWorkflow(
						*options.domain, childWorkflowExecution.ID, childWorkflowExecution.RunID)
				}
			}).AddFuture(mainFuture, func(f Future) {
				// childWorkflow is done, no-op
			}).Select(ctx)
		}
	})

	return result
}

// WorkflowInfo information about currently executing workflow
type WorkflowInfo struct {
	WorkflowExecution                   WorkflowExecution
	WorkflowType                        WorkflowType
	TaskListName                        string
	ExecutionStartToCloseTimeoutSeconds int32
	TaskStartToCloseTimeoutSeconds      int32
	Domain                              string
}

// GetWorkflowInfo extracts info of a current workflow from a context.
func GetWorkflowInfo(ctx Context) *WorkflowInfo {
	return getWorkflowEnvironment(ctx).WorkflowInfo()
}

// GetLogger returns a logger to be used in workflow's context
func GetLogger(ctx Context) *zap.Logger {
	return getWorkflowEnvironment(ctx).GetLogger()
}

// GetMetricsScope returns a metrics scope to be used in workflow's context
func GetMetricsScope(ctx Context) tally.Scope {
	return getWorkflowEnvironment(ctx).GetMetricsScope()
}

// Now returns the current time when the decision is started or replayed.
// The workflow needs to use this Now() to get the wall clock time instead of the Go lang library one.
func Now(ctx Context) time.Time {
	return getWorkflowEnvironment(ctx).Now()
}

// NewTimer returns immediately and the future becomes ready after the specified duration d. The workflow needs to use
// this NewTimer() to get the timer instead of the Go lang library one(timer.NewTimer()). You can cancel the pending
// timer by cancel the Context (using context from workflow.WithCancel(ctx)) and that will cancel the timer. After timer
// is canceled, the returned Future become ready, and Future.Get() will return *CanceledError.
// The current timer resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func NewTimer(ctx Context, d time.Duration) Future {
	future, settable := NewFuture(ctx)
	if d <= 0 {
		settable.Set(true, nil)
		return future
	}

	t := getWorkflowEnvironment(ctx).NewTimer(d, func(r []byte, e error) {
		settable.Set(nil, e)
	})
	if t != nil {
		Go(ctx, func(ctx Context) {
			if ctxDone := ctx.Done(); ctxDone != nil {
				NewSelector(ctx).AddReceive(ctxDone, func(c Channel, more bool) {
					// We will cancel the timer either it is explicit cancellation (or) we are closed.
					getWorkflowEnvironment(ctx).RequestCancelTimer(t.timerID)
				}).AddFuture(future, func(f Future) {
					// timer is done, no-op
				}).Select(ctx)
			}
		})
	}
	return future
}

// Sleep pauses the current workflow for at least the duration d. A negative or zero duration causes Sleep to return
// immediately. Workflow code needs to use this Sleep() to sleep instead of the Go lang library one(timer.Sleep()).
// You can cancel the pending sleep by cancel the Context (using context from workflow.WithCancel(ctx)).
// Sleep() returns nil if the duration d is passed, or it returns *CanceledError if the ctx is canceled. There are 2
// reasons the ctx could be canceled: 1) your workflow code cancel the ctx (with workflow.WithCancel(ctx));
// 2) your workflow itself is canceled by external request.
// The current timer resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func Sleep(ctx Context, d time.Duration) (err error) {
	t := NewTimer(ctx, d)
	err = t.Get(ctx, nil)
	return
}

// RequestCancelWorkflow can be used to request cancellation of an external workflow.
// Input workflowID is the workflow ID of target workflow.
// Input runID indicates the instance of a workflow. Input runID is optional (default is ""). When runID is not specified,
// then the currently running instance of that workflowID will be used.
// By default, the current workflow's domain will be used as target domain. However, you can specify a different domain
// of the target workflow using the context like:
//	ctx := WithWorkflowDomain(ctx, "domain-name")
func RequestCancelWorkflow(ctx Context, workflowID, runID string) error {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	options := getWorkflowEnvOptions(ctx1)
	if options.domain == nil {
		return errors.New("need a valid domain")
	}
	return getWorkflowEnvironment(ctx).RequestCancelWorkflow(*options.domain, workflowID, runID)
}

// WithChildWorkflowOptions adds all workflow options to the context.
func WithChildWorkflowOptions(ctx Context, cwo ChildWorkflowOptions) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	wfOptions := getWorkflowEnvOptions(ctx1)
	wfOptions.domain = common.StringPtr(cwo.Domain)
	wfOptions.taskListName = common.StringPtr(cwo.TaskList)
	wfOptions.workflowID = cwo.WorkflowID
	wfOptions.executionStartToCloseTimeoutSeconds = common.Int32Ptr(int32(cwo.ExecutionStartToCloseTimeout.Seconds()))
	wfOptions.taskStartToCloseTimeoutSeconds = common.Int32Ptr(int32(cwo.TaskStartToCloseTimeout.Seconds()))
	wfOptions.childPolicy = cwo.ChildPolicy
	wfOptions.waitForCancellation = cwo.WaitForCancellation
	wfOptions.workflowIDReusePolicy = cwo.WorkflowIDReusePolicy

	return ctx1
}

// WithWorkflowDomain adds a domain to the context.
func WithWorkflowDomain(ctx Context, name string) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).domain = common.StringPtr(name)
	return ctx1
}

// WithWorkflowTaskList adds a task list to the context.
func WithWorkflowTaskList(ctx Context, name string) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).taskListName = common.StringPtr(name)
	return ctx1
}

// WithWorkflowID adds a workflowID to the context.
func WithWorkflowID(ctx Context, workflowID string) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).workflowID = workflowID
	return ctx1
}

// WithChildPolicy adds a ChildWorkflowPolicy to the context.
func WithChildPolicy(ctx Context, childPolicy ChildWorkflowPolicy) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).childPolicy = childPolicy
	return ctx1
}

// WithExecutionStartToCloseTimeout adds a workflow execution timeout to the context.
func WithExecutionStartToCloseTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).executionStartToCloseTimeoutSeconds = common.Int32Ptr(int32(d.Seconds()))
	return ctx1
}

// WithWorkflowTaskStartToCloseTimeout adds a decision timeout to the context.
func WithWorkflowTaskStartToCloseTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setWorkflowEnvOptionsIfNotExist(ctx)
	getWorkflowEnvOptions(ctx1).taskStartToCloseTimeoutSeconds = common.Int32Ptr(int32(d.Seconds()))
	return ctx1
}

// GetSignalChannel returns channel corresponding to the signal name.
func GetSignalChannel(ctx Context, signalName string) Channel {
	return getWorkflowEnvOptions(ctx).getSignalChannel(ctx, signalName)
}

// Get extract data from encoded data to desired value type. valuePtr is pointer to the actual value type.
func (b EncodedValue) Get(valuePtr interface{}) error {
	return getHostEnvironment().decodeArg(b, valuePtr)
}

// SideEffect executes provided function once, records its result into the workflow history. The recorded result on
// history will be returned without executing the provided function during replay. This guarantees the deterministic
// requirement for workflow as the exact same result will be returned in replay.
// Common use case is to run some short non-deterministic code in workflow, like getting random number or new UUID.
// The only way to fail SideEffect is to panic which causes decision task failure. The decision task after timeout is
// rescheduled and re-executed giving SideEffect another chance to succeed.
//
// Caution: do not use SideEffect to modify closures, always retrieve result from SideEffect's encoded return value.
// For example this code is BROKEN:
//  // Bad example:
//  var random int
//  workflow.SideEffect(func(ctx workflow.Context) interface{} {
//         random = rand.Intn(100)
//         return nil
//  })
//  // random will always be 0 in replay, thus this code is non-deterministic
//  if random < 50 {
//         ....
//  } else {
//         ....
//  }
// On replay the provided function is not executed, the random will always be 0, and the workflow could takes a
// different path breaking the determinism.
//
// Here is the correct way to use SideEffect:
//  // Good example:
//  encodedRandom := SideEffect(func(ctx workflow.Context) interface{} {
//        return rand.Intn(100)
//  })
//  var random int
//  encodedRandom.Get(&random)
//  if random < 50 {
//         ....
//  } else {
//         ....
//  }
func SideEffect(ctx Context, f func(ctx Context) interface{}) encoded.Value {
	future, settable := NewFuture(ctx)
	wrapperFunc := func() ([]byte, error) {
		r := f(ctx)
		return getHostEnvironment().encodeArg(r)
	}
	resultCallback := func(result []byte, err error) {
		settable.Set(EncodedValue(result), err)
	}
	getWorkflowEnvironment(ctx).SideEffect(wrapperFunc, resultCallback)
	var encoded EncodedValue
	if err := future.Get(ctx, &encoded); err != nil {
		panic(err)
	}
	return encoded
}

// DefaultVersion is a version returned by GetVersion for code that wasn't versioned before
const DefaultVersion Version = -1

// GetVersion is used to safely perform backwards incompatible changes to workflow definitions.
// It is not allowed to update workflow code while there are workflows running as it is going to break
// determinism. The solution is to have both old code that is used to replay existing workflows
// as well as the new one that is used when it is executed for the first time.
// GetVersion returns maxSupported version when is executed for the first time. This version is recorded into the
// workflow history as a marker event. Even if maxSupported version is changed the version that was recorded is
// returned on replay. DefaultVersion constant contains version of code that wasn't versioned before.
// For example initially workflow has the following code:
//  err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
// it should be updated to
//  err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
// The backwards compatible way to execute the update is
//  v :=  GetVersion(ctx, "fooChange", DefaultVersion, 1)
//  if v  == DefaultVersion {
//      err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
//  } else {
//      err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//  }
//
// Then bar has to be changed to baz:
//  v :=  GetVersion(ctx, "fooChange", DefaultVersion, 2)
//  if v  == DefaultVersion {
//      err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
//  } else if v == 1 {
//      err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//  } else {
//      err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//  }
//
// Later when there are no workflow executions running DefaultVersion the correspondent branch can be removed:
//  v :=  GetVersion(ctx, "fooChange", 1, 2)
//  if v == 1 {
//      err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//  } else {
//      err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//  }
//
// Currently there is no supported way to completely remove GetVersion call after it was introduced.
// Keep it even if single branch is left:
//  GetVersion(ctx, "fooChange", 2, 2)
//  err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//
// It is necessary as GetVersion performs validation of a version against a workflow history and fails decisions if
// a workflow code is not compatible with it.
func GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version {
	return getWorkflowEnvironment(ctx).GetVersion(changeID, minSupported, maxSupported)
}

// SetQueryHandler sets the query handler to handle workflow query. The queryType specify which query type this handler
// should handle. The handler must be a function that returns 2 values. The first return value must be a serializable
// result. The second return value must be an error. The handler function could receive any number of input parameters.
// All the input parameter must be serializable. You should call workflow.SetQueryHandler() at the beginning of the workflow
// code. When client calls Client.QueryWorkflow() to cadence server, a task will be generated on server that will be dispatched
// to a workflow worker, which will replay the history events and then execute a query handler based on the query type.
// The query handler will be invoked out of the context of the workflow, meaning that the handler code must not use cadence
// context to do things like workflow.NewChannel(), workflow.Go() or to call any workflow blocking functions like
// Channel.Get() or Future.Get(). Trying to do so in query handler code will fail the query and client will receive
// QueryFailedError.
// Example of workflow code that support query type "current_state":
//  func MyWorkflow(ctx workflow.Context, input string) error {
//    currentState := "started" // this could be any serializable struct
//    err := workflow.SetQueryHandler(ctx, "current_state", func() (string, error) {
//      return currentState, nil
//    })
//    if err != nil {
//      currentState = "failed to register query handler"
//      return err
//    }
//    // your normal workflow code begins here, and you update the currentState as the code makes progress.
//    currentState = "waiting timer"
//    err = NewTimer(ctx, time.Hour).Get(ctx, nil)
//    if err != nil {
//      currentState = "timer failed"
//      return err
//    }
//
//    currentState = "waiting activity"
//    ctx = WithActivityOptions(ctx, myActivityOptions)
//    err = ExecuteActivity(ctx, MyActivity, "my_input").Get(ctx, nil)
//    if err != nil {
//      currentState = "activity failed"
//      return err
//    }
//    currentState = "done"
//    return nil
//  }
func SetQueryHandler(ctx Context, queryType string, handler interface{}) error {
	if strings.HasPrefix(queryType, "__") {
		return errors.New("queryType starts with '__' is reserved for internal use")
	}
	return setQueryHandler(ctx, queryType, handler)
}
