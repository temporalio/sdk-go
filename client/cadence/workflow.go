package cadence

import (
	"fmt"

	"time"

	"github.com/pborman/uuid"
)

// Channel must be used instead of native go channel by workflow code.
// Use Context.NewChannel method to create an instance.
type Channel interface {
	Receive(ctx Context) (v interface{}, more bool)    // more is false when channel is closed
	ReceiveAsync() (v interface{}, ok bool, more bool) // ok is true when value was returned, more is false when channel is closed

	Send(ctx Context, v interface{})
	SendAsync(v interface{}) (ok bool) // ok when value was sent
	Close()                            // prohibit sends
}

// ReceiveCaseFunc is executed when a value is received from the corresponding channel
type ReceiveCaseFunc func(v interface{}, more bool)

// SendCaseFunc is executed when value was sent to a correspondent channel
type SendCaseFunc func()

// DefaultCaseFunc is executed when none of the channel cases executed
type DefaultCaseFunc func()

// FutureCaseFunc is executed when future becomes ready.
// Parameters are value and error that future contains.
type FutureCaseFunc func(v interface{}, err error)

// Selector must be used instead of native go select by workflow code
// Use Context.NewSelector method to create an instance.
type Selector interface {
	AddReceive(c Channel, f ReceiveCaseFunc) Selector
	AddSend(c Channel, v interface{}, f SendCaseFunc) Selector
	AddFuture(future Future, f FutureCaseFunc) Selector
	AddDefault(f DefaultCaseFunc)
	Select(ctx Context)
}

// Future represents the result of an asynchronous computation.
type Future interface {
	Get(ctx Context) (interface{}, error)
	IsReady() bool
}

// Settable is used to set value or error on a future.
// See NewFuture function.
type Settable interface {
	Set(value interface{}, err error)
	SetValue(value interface{})
	SetError(err error)
	Chain(future Future) // Value (or error) of the future become the same of the chained one.
}

// Func is a body of a coroutine which should be used instead of goroutines by the workflow code
type Func func(ctx Context)

// PanicError contains information about panicked workflow
type PanicError interface {
	error
	Value() interface{} // Value passed to panic call
	StackTrace() string // Stack trace of a panicked coroutine
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
func Go(ctx Context, f Func) {
	state := getState(ctx)
	state.dispatcher.newCoroutine(ctx, f)
}

// GoNamed creates a new coroutine with a given human readable name.
// It has similar semantic to goroutine in a context of the workflow.
// Name appears in stack traces that are blocked on this Channel.
func GoNamed(ctx Context, name string, f Func) {
	state := getState(ctx)
	state.dispatcher.newNamedCoroutine(ctx, name, f)
}

// Error to return from Workflow and Activity implementations.
type Error interface {
	error
	Reason() string
	Details() []byte
}

// NewError creates Error instance
func NewError(reason string, details []byte) Error {
	return &errorImpl{reason: reason, details: details}
}

// NewFuture creates a new future as well as associated Settable that is used to set its value.
func NewFuture(ctx Context) (Future, Settable) {
	impl := &futureImpl{channel: NewChannel(ctx)}
	return impl, impl
}

// Workflow is an interface that any workflow should implement.
// Code of a workflow must be deterministic. It must use cadence.Channel, cadence.Selector, and cadence.Go instead of
// native channels, select and go. It also must not use range operation over map as it is randomized by go runtime.
// All time manipulation should use current time returned by GetTime(ctx) method.
// Note that cadence.Context is used instead of context.Context to avoid use of raw channels.
type Workflow interface {
	Execute(ctx Context, input []byte) (result []byte, err error)
}

// ExecuteActivityParameters configuration parameters for scheduling an activity
type ExecuteActivityParameters struct {
	ActivityID                    *string // Users can choose IDs but our framework makes it optional to decrease the crust.
	ActivityType                  ActivityType
	TaskListName                  string
	Input                         []byte
	ScheduleToCloseTimeoutSeconds int32
	ScheduleToStartTimeoutSeconds int32
	StartToCloseTimeoutSeconds    int32
	HeartbeatTimeoutSeconds       int32
	WaitForCancellation           bool
}

// ExecuteActivity requests activity execution in the context of a workflow.
func ExecuteActivity(ctx Context, parameters ExecuteActivityParameters) (result []byte, err error) {
	channelName := fmt.Sprintf("\"activity %v\"", parameters.ActivityID)
	resultChannel := NewNamedBufferedChannel(ctx, channelName, 1)
	a := getWorkflowEnvironment(ctx).ExecuteActivity(parameters, func(r []byte, e Error) {
		result = r
		if e != nil {
			err = e.(Error)
		}
		ok := resultChannel.SendAsync(true)
		if !ok {
			panic("unexpected")
		}
		executeDispatcher(ctx, getDispatcher(ctx))
	})
	Go(ctx, func(ctx Context) {
		if ctx.Done() == nil {
			return // not cancellable.
		}
		if ctx.Done().Receive(ctx); ctx.Err() == ErrCanceled {
			getWorkflowEnvironment(ctx).RequestCancelActivity(a.activityID)
		}
	})
	_, _ = resultChannel.Receive(ctx)
	return
}

// ExecuteActivityAsync requests activity execution in the context of a workflow.
func ExecuteActivityAsync(ctx Context, parameters ExecuteActivityParameters) Future {
	future, settable := NewFuture(ctx)
	a := getWorkflowEnvironment(ctx).ExecuteActivity(parameters, func(r []byte, e Error) {
		var err Error
		if e != nil {
			err = e.(Error)
		}
		settable.Set(r, err)
		executeDispatcher(ctx, getDispatcher(ctx))
	})
	Go(ctx, func(ctx Context) {
		if ctx.Done() == nil {
			return // not cancellable.
		}
		if ctx.Done().Receive(ctx); ctx.Err() == ErrCanceled {
			getWorkflowEnvironment(ctx).RequestCancelActivity(a.activityID)
		}
	})
	return future
}

// WorkflowInfo information about currently executing workflow
type WorkflowInfo struct {
	WorkflowExecution WorkflowExecution
	WorkflowType      WorkflowType
	TaskListName      string
}

// GetWorkflowInfo extracts info of a current workflow from a context.
func GetWorkflowInfo(ctx Context) *WorkflowInfo {
	return getWorkflowEnvironment(ctx).WorkflowInfo()
}

// Now returns the current time when the decision is started or replayed.
// The workflow needs to use this Now() to get the wall clock time instead of the Go lang library one.
func Now(ctx Context) time.Time {
	return getWorkflowEnvironment(ctx).Now()
}

// Timer - represents a timer for workflows.
// https://golang.org/pkg/time/#Timer
// The Timer type represents a single event.
// When the Timer expires, the signal will be sent on C.
// A Timer must be created with NewTimer.
type Timer struct {
	C Channel
}

// NewTimer returns after the specified delay expires.
// The current timer resolution implementation is in seconds but is subjected to change.
// The workflow needs to use this NewTimer() to get the timer instead of the Go lang library one(timer.NewTimer())
func NewTimer(ctx Context, d time.Duration) (t *Timer, err error) {
	if d <= 0 {
		ch := &channelImpl{size: 1}
		ch.SendAsync(true)
		return &Timer{C: ch}, nil
	}

	channelName := fmt.Sprintf("\"timer %v\"", uuid.New())
	resultChannel := NewNamedBufferedChannel(ctx, channelName, 1)
	getWorkflowEnvironment(ctx).NewTimer(d, func(r []byte, e Error) {
		err = e
		ok := resultChannel.SendAsync(true)
		if !ok {
			panic("unexpected")
		}
		executeDispatcher(ctx, getDispatcher(ctx))
	})
	return &Timer{C: resultChannel}, err
}

// Stop prevents the Timer from firing.
// https://golang.org/pkg/time/#Timer.Stop
// It returns true if the call stops the timer, false if the timer has already
// expired or been stopped.
// Stop does not close the channel, to prevent a read from the channel succeeding
// incorrectly.
//
// To prevent the timer firing after a call to Stop,
// check the return value and drain the channel. For example:
// 	if !t.Stop() {
// 		<-t.C
// 	}
// This cannot be done concurrent to other receives from the Timer's
// channel.
func (wt *Timer) Stop() bool {
	panic("TODO: Stop to be implemented")
}
