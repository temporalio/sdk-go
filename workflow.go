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

import (
	"errors"
	"fmt"
	"time"

	"go.uber.org/cadence/common"
	"go.uber.org/zap"
)

var (
	errActivityParamsBadRequest = errors.New("missing activity parameters through context, check ActivityOptions")
	errWorkflowOptionBadRequest = errors.New("missing workflow options through context, check WorkflowOptions")
)

type (

	// Channel must be used instead of native go channel by workflow code.
	// Use Context.NewChannel method to create an instance.
	Channel interface {
		// Blocks until it gets a value. when it gets a value assigns to the provided pointer.
		// Example:
		//   var v string
		//   c.Receive(ctx, &v)
		Receive(ctx Context, valuePtr interface{}) (more bool)              // more is false when channel is closed
		ReceiveAsync(valuePtr interface{}) (ok bool)                        // ok is true when value was returned
		ReceiveAsyncWithMoreFlag(valuePtr interface{}) (ok bool, more bool) // ok is true when value was returned, more is false when channel is closed
		Send(ctx Context, v interface{})
		SendAsync(v interface{}) (ok bool) // ok when value was sent
		Close()                            // prohibit sends
	}

	// Selector must be used instead of native go select by workflow code
	// Use Context.NewSelector method to create an instance.
	Selector interface {
		AddReceive(c Channel, f func(c Channel, more bool)) Selector
		AddSend(c Channel, v interface{}, f func()) Selector
		AddFuture(future Future, f func(f Future)) Selector
		AddDefault(f func())
		Select(ctx Context)
	}

	// Future represents the result of an asynchronous computation.
	Future interface {
		// Get blocks until the future is ready. When ready it either returns non nil error
		// or assigns result value to the provided pointer.
		// Example:
		// var v string
		// if err := f.Get(ctx, &v); err != nil {
		//     return err
		// }
		// fmt.Printf("Value=%v", v)
		Get(ctx Context, valuePtr interface{}) error
		// When true Get is guaranteed to not block
		IsReady() bool
	}

	// Settable is used to set value or error on a future.
	// See NewFuture function.
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

// RegisterWorkflow - registers a workflow function with the framework.
// A workflow takes a cadence context and input and returns a (result, error) or just error.
// Examples:
//	func sampleWorkflow(ctx cadence.Context, input []byte) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context, arg1 int, arg2 string) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context) (result []byte, err error)
//	func sampleWorkflow(ctx cadence.Context, arg1 int) (result string, err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
// This method calls panic if workflowFunc doesn't comply with the expected format.
func RegisterWorkflow(workflowFunc interface{}) {
	thImpl := getHostEnvironment()
	err := thImpl.RegisterWorkflow(workflowFunc)
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
//  - Context can be used to pass the settings for this activity.
// 	For example: task list that this need to be routed, timeouts that need to be configured.
//	Use ActivityOptions to pass down the options.
//			ao := ActivityOptions{
// 				TaskList: "exampleTaskList",
// 				ScheduleToStartTimeout: 10 * time.Second,
// 				StartToCloseTimeout: 5 * time.Second,
// 				ScheduleToCloseTimeout: 10 * time.Second,
// 				HeartbeatTimeout: 0,
// 			}
//			ctx1 := WithActivityOptions(ctx, ao)
//
//			or to override a single option
//
//			ctx1 := WithTaskList(ctx, "exampleTaskList")
//  - f - Either a activity name or a function that is getting scheduled.
//  - args - The arguments that need to be passed to the function represented by 'f'.
//  - If the activity failed to complete then the future get error would indicate the failure
// and it can be one of ErrorWithDetails, TimeoutError, CanceledError.
//  - You can also cancel the pending activity using context(WithCancel(ctx)) and that will fail the activity with
// error CanceledError.
// - returns Future with activity result or failure
func ExecuteActivity(ctx Context, f interface{}, args ...interface{}) Future {
	// Validate type and its arguments.
	future, settable := newDecodeFuture(ctx, f)
	activityType, input, err := getValidatedActivityFunction(f, args)
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
		if ctx.Done() == nil {
			return // not cancellable.
		}
		if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
			getWorkflowEnvironment(ctx).RequestCancelActivity(a.activityID)
		}
	})
	return future
}

// ExecuteChildWorkflow requests child workflow execution in the context of a workflow.
//  - Context can be used to pass the settings for the child workflow.
// 	For example: task list that this child workflow should be routed, timeouts that need to be configured.
//	Use ChildWorkflowOptions to pass down the options.
//			cwo := ChildWorkflowOptions{
// 				ExecutionStartToCloseTimeout: 10 * time.Minute,
// 				TaskStartToCloseTimeout: time.Minute,
// 			}
//			ctx1 := WithChildWorkflowOptions(ctx, cwo)
//  - f - Either a workflow name or a workflow function that is getting scheduled.
//  - args - The arguments that need to be passed to the child workflow function represented by 'f'.
//  - If the child workflow failed to complete then the future get error would indicate the failure
// and it can be one of ErrorWithDetails, TimeoutError, CanceledError.
//  - You can also cancel the pending child workflow using context(WithCancel(ctx)) and that will fail the workflow with
// error CanceledError.
// - returns ChildWorkflowFuture
func ExecuteChildWorkflow(ctx Context, f interface{}, args ...interface{}) ChildWorkflowFuture {
	mainFuture, mainSettable := newDecodeFuture(ctx, f)
	executionFuture, executionSettable := NewFuture(ctx)
	result := childWorkflowFutureImpl{
		decodeFutureImpl: mainFuture.(*decodeFutureImpl),
		executionFuture:  executionFuture.(*futureImpl)}
	wfType, input, err := getValidatedWorkerFunction(f, args)
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
		if ctx.Done() == nil {
			return // not cancellable.
		}
		if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
			if childWorkflowExecution != nil {
				getWorkflowEnvironment(ctx).RequestCancelWorkflow(
					*options.domain, childWorkflowExecution.ID, childWorkflowExecution.RunID)
			}
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

// Now returns the current time when the decision is started or replayed.
// The workflow needs to use this Now() to get the wall clock time instead of the Go lang library one.
func Now(ctx Context) time.Time {
	return getWorkflowEnvironment(ctx).Now()
}

// NewTimer returns immediately and the future becomes ready after the specified timeout.
//  - The current timer resolution implementation is in seconds but is subjected to change.
//  - The workflow needs to use this NewTimer() to get the timer instead of the Go lang library one(timer.NewTimer())
//  - You can also cancel the pending timer using context(WithCancel(ctx)) and that will cancel the timer with
// error TimerCanceledError.
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
			if ctx.Done() == nil {
				return // not cancellable.
			}
			// We will cancel the timer either it is explicit cancellation
			// (or) we are closed.
			ctx.Done().Receive(ctx, nil)
			getWorkflowEnvironment(ctx).RequestCancelTimer(t.timerID)
		})
	}
	return future
}

// Sleep pauses the current goroutine for at least the duration d.
// A negative or zero duration causes Sleep to return immediately.
//  - The current timer resolution implementation is in seconds but is subjected to change.
//  - The workflow needs to use this Sleep() to sleep instead of the Go lang library one(timer.Sleep())
//  - You can also cancel the pending sleep using context(WithCancel(ctx)) and that will cancel the sleep with
//    error TimerCanceledError.
func Sleep(ctx Context, d time.Duration) (err error) {
	t := NewTimer(ctx, d)
	err = t.Get(ctx, nil)
	return
}

// RequestCancelWorkflow can be used to request cancellation of an external workflow.
// - workflowID - name of the workflow ID.
// - runID 	- Optional - indicates the instance of a workflow.
// You can specify the domain of the workflow using the context like
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

// SideEffect executes provided function once, records its result into the workflow history and doesn't
// reexecute it on replay returning recorded result instead. It can be seen as an "inline" activity.
// Use it only for short nondeterministic code snippets like getting random value or generating UUID.
// The only way to fail SideEffect is to panic which causes decision task failure. The decision task after timeout is
// rescheduled and reexecuted giving SideEffect another chance to succeed.
// Be careful to not return any data from SideEffect function any other way than through its recorded return value.
// For example this code is BROKEN:
//
// var executed bool
// cadence.SideEffect(func(ctx cadence.Context) interface{} {
//        executed = true
//        return nil
// })
// if executed {
//        ....
// } else {
//        ....
// }
// On replay the function is not executed, the executed flag is not set to true
// and the workflow takes a different path breaking the determinism.
//
// Here is the correct way to use SideEffect:
//
// encodedRandom := SideEffect(func(ctx cadence.Context) interface{} {
//       return rand.Intn(100)
// })
// var random int
// encodedRandom.Get(&random)
// if random < 50 {
//        ....
// } else {
//        ....
// }
func SideEffect(ctx Context, f func(ctx Context) interface{}) EncodedValue {
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
