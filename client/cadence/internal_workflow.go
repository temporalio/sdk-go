package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

const workflowEnvironmentContextKey = "workflowEnv"
const workflowResultContextKey = "workflowResult"

// Pointer to pointer to workflow result
func getWorkflowResultPointerPointer(ctx Context) **workflowResult {
	rpp := ctx.Value(workflowResultContextKey)
	if rpp == nil {
		panic("getWorkflowResultPointerPointer: Not a workflow context")
	}
	return rpp.(**workflowResult)
}

func getWorkflowEnvironment(ctx Context) workflowEnvironment {
	wc := ctx.Value(workflowEnvironmentContextKey)
	if wc == nil {
		panic("getWorkflowContext: Not a workflow context")
	}
	return wc.(workflowEnvironment)
}

type syncWorkflowDefinition struct {
	workflow   Workflow
	dispatcher dispatcher
}

type workflowResult struct {
	workflowResult []byte
	error          error
}

type activityClient struct {
	dispatcher  dispatcher
	asyncClient asyncActivityClient
}

type futureImpl struct {
	value   interface{}
	err     error
	ready   bool
	channel Channel
	chained []*futureImpl // Futures that are chained to this one
}

func (f *futureImpl) Get(ctx Context) (interface{}, error) {
	_, more := f.channel.ReceiveWithMoreFlag(ctx)
	if more {
		panic("not closed")
	}
	if !f.ready {
		panic("not ready")
	}
	return f.value, f.err
}

func (f *futureImpl) IsReady() bool {
	return f.ready
}

func (f *futureImpl) Set(value interface{}, err error) {
	if f.ready {
		panic("already set")
	}
	f.value = value
	f.err = err
	f.ready = true
	f.channel.Close()
	for _, ch := range f.chained {
		ch.Set(f.value, f.err)
	}
}

func (f *futureImpl) SetValue(value interface{}) {
	if f.ready {
		panic("already set")
	}
	f.Set(value, nil)
}

func (f *futureImpl) SetError(err error) {
	if f.ready {
		panic("already set")
	}
	f.Set(nil, err)
}

func (f *futureImpl) Chain(future Future) {
	if f.ready {
		panic("already set")
	}
	ch, ok := future.(*futureImpl)
	if !ok {
		panic("cannot chain Future that wasn't created with cadence.NewFuture")
	}
	if !ch.IsReady() {
		ch.chained = append(ch.chained, f)
		return
	}
	f.value = ch.value
	f.err = ch.err
	f.ready = true
	return
}

func (d *syncWorkflowDefinition) Execute(env workflowEnvironment, input []byte) {
	ctx := WithValue(background, workflowEnvironmentContextKey, env)
	var resultPtr *workflowResult
	ctx = WithValue(ctx, workflowResultContextKey, &resultPtr)

	d.dispatcher = newDispatcher(ctx, func(ctx Context) {
		r := &workflowResult{}
		r.workflowResult, r.error = d.workflow.Execute(ctx, input)
		rpp := getWorkflowResultPointerPointer(ctx)
		*rpp = r
	})
	executeDispatcher(ctx, d.dispatcher)
}

func (d *syncWorkflowDefinition) StackTrace() string {
	return d.dispatcher.StackTrace()
}

func (d *syncWorkflowDefinition) Close() {
	if d.dispatcher != nil {
		d.dispatcher.Close()
	}
}

// Dispatcher is a container of a set of coroutines.
type dispatcher interface {
	// ExecuteUntilAllBlocked executes coroutines one by one in deterministic order
	// until all of them are completed or blocked on Channel or Selector
	ExecuteUntilAllBlocked() (err PanicError)
	// IsDone returns true when all of coroutines are completed
	IsDone() bool
	Close()             // Destroys all coroutines without waiting for their completion
	StackTrace() string // Stack trace of all coroutines owned by the Dispatcher instance
}

// NewDispatcher creates a new Dispatcher instance with a root coroutine function.
// Context passed to the root function is child of the passed rootCtx.
// This way rootCtx can be used to pass values to the coroutine code.
func newDispatcher(rootCtx Context, root Func) dispatcher {
	result := &dispatcherImpl{}
	result.newCoroutine(rootCtx, root)
	return result
}

// getDispatcher retrieves current dispatcher from the Context passed to the coroutine function.
func getDispatcher(ctx Context) dispatcher {
	return getState(ctx).dispatcher
}

// executeDispatcher executed coroutines in the calling thread and calls workflow completion callbacks
// if root workflow function returned
func executeDispatcher(ctx Context, dispatcher dispatcher) {
	panicErr := dispatcher.ExecuteUntilAllBlocked()
	if panicErr != nil {
		getWorkflowEnvironment(ctx).Complete(
			nil,
			NewErrorWithDetails(panicErr.Error(), []byte(panicErr.StackTrace())),
		)
		dispatcher.Close()
		return
	}
	rp := *getWorkflowResultPointerPointer(ctx)
	if rp == nil {
		// Result is not set, so workflow is still executing
		return
	}
	// Cannot cast nil values from interface{} to interface
	getWorkflowEnvironment(ctx).Complete(rp.workflowResult, rp.error)
	dispatcher.Close()
}

// For troubleshooting stack pretty printing only.
// Set to true to see full stack trace that includes framework methods.
const disableCleanStackTraces = false

type valueCallbackPair struct {
	value    interface{}
	callback func()
}

type channelImpl struct {
	name            string              // human readable channel name
	size            int                 // Channel buffer size. 0 for non buffered.
	buffer          []interface{}       // buffered messages
	blockedSends    []valueCallbackPair // puts waiting when buffer is full.
	blockedReceives []func(interface{}) // receives waiting when no messages are available.
	closed          bool                // true if channel is closed
}

// Single case statement of the Select
type selectCase struct {
	channel                 Channel                         // Channel of this case.
	receiveFunc             *func(v interface{})            // function to call when channel has a message. nil for send case.
	receiveWithMoreFlagFunc *func(v interface{}, more bool) // function to call when channel has a message. nil for send case.

	sendFunc   *func()                         // function to call when channel accepted a message. nil for receive case.
	sendValue  *interface{}                    // value to send to the channel. Used only for send case.
	future     Future                          // Used for future case
	futureFunc *func(v interface{}, err error) // function to call when Future is ready
}

// Implements Selector interface
type selectorImpl struct {
	name        string
	cases       []selectCase // cases that this select is comprised from
	defaultFunc *func()      // default case
}

// unblockFunc is passed evaluated by a coroutine yield. When it returns false the yield returns to a caller.
// stackDepth is the depth of stack from the last blocking call relevant to user.
// Used to truncate internal stack frames from thread stack.
type unblockFunc func(status string, stackDepth int) (keepBlocked bool)

type coroutineState struct {
	name         string
	dispatcher   *dispatcherImpl  // dispatcher this context belongs to
	aboutToBlock chan bool        // used to notify dispatcher that coroutine that owns this context is about to block
	unblock      chan unblockFunc // used to notify coroutine that it should continue executing.
	keptBlocked  bool             // true indicates that coroutine didn't make any progress since the last yield unblocking
	closed       bool             // indicates that owning coroutine has finished execution
	panicError   PanicError       // non nil if coroutine had unhandled panic
}

type panicError struct {
	value      interface{}
	stackTrace string
}

type dispatcherImpl struct {
	sequence         int
	channelSequence  int // used to name channels
	selectorSequence int // used to name channels
	coroutines       []*coroutineState
	executing        bool       // currently running ExecuteUntilAllBlocked. Used to avoid recursive calls to it.
	mutex            sync.Mutex // used to synchronize executing
	closed           bool
}

// Assert that structs do indeed implement the interfaces
var _ Channel = (*channelImpl)(nil)
var _ Selector = (*selectorImpl)(nil)
var _ dispatcher = (*dispatcherImpl)(nil)

const coroutinesContextKey = "coroutines"

func getState(ctx Context) *coroutineState {
	s := ctx.Value(coroutinesContextKey)
	if s == nil {
		panic("getState: not workflow context")
	}
	return s.(*coroutineState)
}

func (c *channelImpl) Receive(ctx Context) (v interface{}) {
	v, _ = c.ReceiveWithMoreFlag(ctx)
	return v
}

func (c *channelImpl) ReceiveWithMoreFlag(ctx Context) (v interface{}, more bool) {
	state := getState(ctx)
	hasResult := false
	var result interface{}
	for {
		if hasResult {
			state.unblocked()
			return result, true
		}
		if len(c.buffer) > 0 {
			r := c.buffer[0]
			c.buffer = c.buffer[1:]
			state.unblocked()
			return r, true
		}
		if c.closed {
			return nil, false
		}
		if len(c.blockedSends) > 0 {
			b := c.blockedSends[0]
			c.blockedSends = c.blockedSends[1:]
			b.callback()
			state.unblocked()
			return b.value, true
		}
		c.blockedReceives = append(c.blockedReceives, func(v interface{}) {
			result = v
			hasResult = true
		})
		state.yield(fmt.Sprintf("blocked on %s.Receive", c.name))
	}
}

func (c *channelImpl) ReceiveAsync() (v interface{}, ok bool) {
	v, ok, _ = c.ReceiveAsyncWithMoreFlag()
	return v, ok
}

func (c *channelImpl) ReceiveAsyncWithMoreFlag() (v interface{}, ok bool, more bool) {
	if len(c.buffer) > 0 {
		r := c.buffer[0]
		c.buffer = c.buffer[1:]
		return r, true, true
	}
	if c.closed {
		return nil, false, false
	}
	if len(c.blockedSends) > 0 {
		b := c.blockedSends[0]
		c.blockedSends = c.blockedSends[1:]
		b.callback()
		return b.value, true, true
	}
	return nil, false, true
}

func (c *channelImpl) Send(ctx Context, v interface{}) {
	state := getState(ctx)
	valueConsumed := false
	for {
		// Check for closed in the loop as close can be called when send is blocked
		if c.closed {
			panic("Closed channel")
		}
		if valueConsumed {
			state.unblocked()
			return
		}
		if len(c.buffer) < c.size {
			c.buffer = append(c.buffer, v)
			state.unblocked()
			return
		}
		if len(c.blockedReceives) > 0 {
			blockedGet := c.blockedReceives[0]
			c.blockedReceives = c.blockedReceives[1:]
			blockedGet(v)
			state.unblocked()
			return
		}
		c.blockedSends = append(c.blockedSends,
			valueCallbackPair{value: v, callback: func() { valueConsumed = true }})
		state.yield(fmt.Sprintf("blocked on %s.Send", c.name))
	}
}

func (c *channelImpl) SendAsync(v interface{}) (ok bool) {
	if c.closed {
		panic("Closed channel")
	}
	if len(c.buffer) < c.size {
		c.buffer = append(c.buffer, v)
		return true
	}
	if len(c.blockedReceives) > 0 {
		blockedGet := c.blockedReceives[0]
		c.blockedReceives = c.blockedReceives[1:]
		blockedGet(v)
		return true
	}
	return false
}

func (c *channelImpl) Close() {
	c.closed = true
	// All blocked sends are going to panic
	for i := 0; i < len(c.blockedSends); i++ {
		b := c.blockedSends[i]
		b.callback()
	}
}

// initialYield called at the beginning of the coroutine execution
// stackDepth is the depth of top of the stack to omit when stack trace is generated
// to hide frames internal to the framework.
func (s *coroutineState) initialYield(stackDepth int, status string) {
	keepBlocked := true
	for keepBlocked {
		f := <-s.unblock
		keepBlocked = f(status, stackDepth+1)
	}
}

// yield indicates that coroutine cannot make progress and should sleep
// this call blocks
func (s *coroutineState) yield(status string) {
	s.aboutToBlock <- true
	s.initialYield(3, status) // omit three levels of stack. To adjust change to 0 and count the lines to remove.
	s.keptBlocked = true
}

func getStackTrace(coroutineName, status string, stackDepth int) string {
	stack := stackBuf[:runtime.Stack(stackBuf[:], false)]
	rawStack := fmt.Sprintf("%s", strings.TrimRightFunc(string(stack), unicode.IsSpace))
	if disableCleanStackTraces {
		return rawStack
	}
	lines := strings.Split(rawStack, "\n")
	// Omit top stackDepth frames + top status line.
	// Omit bottom two frames which is wrapping of coroutine in a goroutine.
	lines = lines[stackDepth*2+1 : len(lines)-4]
	top := fmt.Sprintf("coroutine %s [%s]:", coroutineName, status)
	lines = append([]string{top}, lines...)
	return strings.Join(lines, "\n")
}

// unblocked is called by coroutine to indicate that since the last time yield was unblocked channel or select
// where unblocked versus calling yield again after checking their condition
func (s *coroutineState) unblocked() {
	s.keptBlocked = false
}

func (s *coroutineState) call() {
	s.unblock <- func(status string, stackDepth int) bool {
		return false // unblock
	}
	<-s.aboutToBlock
}

func (s *coroutineState) close() {
	s.closed = true
	s.aboutToBlock <- true
}

func (s *coroutineState) exit() {
	if !s.closed {
		s.unblock <- func(status string, stackDepth int) bool {
			runtime.Goexit()
			return true
		}
	}
}

var stackBuf [100000]byte

func (s *coroutineState) stackTrace() string {
	if s.closed {
		return ""
	}
	stackCh := make(chan string, 1)
	s.unblock <- func(status string, stackDepth int) bool {
		stackCh <- getStackTrace(s.name, status, stackDepth+1)
		return true
	}
	return <-stackCh
}

func (s *coroutineState) NewCoroutine(ctx Context, f Func) {
	s.dispatcher.newCoroutine(ctx, f)
}

func (s *coroutineState) NewNamedCoroutine(ctx Context, name string, f Func) {
	s.dispatcher.newNamedCoroutine(ctx, name, f)
}

func (s *coroutineState) NewSelector() Selector {
	s.dispatcher.selectorSequence++
	return s.NewNamedSelector(fmt.Sprintf("selector-%v", s.dispatcher.selectorSequence))
}

func (s *coroutineState) NewNamedSelector(name string) Selector {
	return &selectorImpl{name: name}
}

func (s *coroutineState) NewChannel() Channel {
	s.dispatcher.channelSequence++
	return s.NewNamedChannel(fmt.Sprintf("chan-%v", s.dispatcher.channelSequence))
}

func (s *coroutineState) NewNamedChannel(name string) Channel {
	return &channelImpl{name: name}
}

func (s *coroutineState) NewBufferedChannel(size int) Channel {
	return &channelImpl{size: size}
}

func (s *coroutineState) NewNamedBufferedChannel(name string, size int) Channel {
	return &channelImpl{name: name, size: size}
}

func (e *panicError) Error() string {
	return fmt.Sprintf("%v", e.value)
}

func (e *panicError) Value() interface{} {
	return e.value
}

func (e *panicError) StackTrace() string {
	return e.stackTrace
}

func (d *dispatcherImpl) newCoroutine(ctx Context, f Func) {
	d.newNamedCoroutine(ctx, fmt.Sprintf("%v", d.sequence+1), f)
}

func (d *dispatcherImpl) newNamedCoroutine(ctx Context, name string, f Func) {
	state := d.newState(name)
	spawned := WithValue(ctx, coroutinesContextKey, state)
	go func(crt *coroutineState) {
		defer crt.close()
		defer func() {
			if r := recover(); r != nil {
				st := getStackTrace(name, "panic", 3)
				crt.panicError = &panicError{value: r, stackTrace: st}
			}
		}()
		crt.initialYield(1, "")
		f(spawned)
	}(state)
}

func (d *dispatcherImpl) newState(name string) *coroutineState {
	c := &coroutineState{
		name:         name,
		dispatcher:   d,
		aboutToBlock: make(chan bool, 1),
		unblock:      make(chan unblockFunc),
	}
	d.sequence++
	d.coroutines = append(d.coroutines, c)
	return c
}

func (d *dispatcherImpl) ExecuteUntilAllBlocked() (err PanicError) {
	d.mutex.Lock()
	if d.closed {
		panic("dispatcher is closed")
	}
	if d.executing {
		panic("call to ExecuteUntilAllBlocked (possibly from a coroutine) while it is already running")
	}
	d.executing = true
	d.mutex.Unlock()
	defer func() { d.executing = false }()
	allBlocked := false
	// Keep executing until at least one goroutine made some progress
	for !allBlocked {
		// Give every coroutine chance to execute removing closed ones
		allBlocked = true
		lastSequence := d.sequence
		for i := 0; i < len(d.coroutines); i++ {
			c := d.coroutines[i]
			if !c.closed {
				// TODO: Support handling of panic in a coroutine by dispatcher.
				// TODO: Dump all outstanding coroutines if one of them panics
				c.call()
			}
			// c.call() can close the context so check again
			if c.closed {
				// remove the closed one from the slice
				d.coroutines = append(d.coroutines[:i],
					d.coroutines[i+1:]...)
				i--
				if c.panicError != nil {
					return c.panicError
				}
				allBlocked = false

			} else {
				allBlocked = allBlocked && (c.keptBlocked || c.closed)
			}
		}
		// Set allBlocked to false if new coroutines where created
		allBlocked = allBlocked && lastSequence == d.sequence
		if len(d.coroutines) == 0 {
			break
		}
	}
	return nil
}

func (d *dispatcherImpl) IsDone() bool {
	return len(d.coroutines) == 0
}

func (d *dispatcherImpl) Close() {
	d.mutex.Lock()
	d.closed = true
	d.mutex.Unlock()
	for i := 0; i < len(d.coroutines); i++ {
		c := d.coroutines[i]
		if !c.closed {
			c.exit()
		}
	}
}

func (d *dispatcherImpl) StackTrace() string {
	var result string
	for i := 0; i < len(d.coroutines); i++ {
		c := d.coroutines[i]
		if !c.closed {
			if len(result) > 0 {
				result += "\n\n"
			}
			result += c.stackTrace()
		}
	}
	return result
}

func (s *selectorImpl) AddReceive(c Channel, f func(v interface{})) Selector {
	s.cases = append(s.cases, selectCase{channel: c, receiveFunc: &f})
	return s
}

func (s *selectorImpl) AddReceiveWithMoreFlag(c Channel, f func(v interface{}, more bool)) Selector {
	s.cases = append(s.cases, selectCase{channel: c, receiveWithMoreFlagFunc: &f})
	return s
}

func (s *selectorImpl) AddSend(c Channel, v interface{}, f func()) Selector {
	s.cases = append(s.cases, selectCase{channel: c, sendFunc: &f, sendValue: &v})
	return s
}

func (s *selectorImpl) AddFuture(future Future, f func(v interface{}, err error)) Selector {
	s.cases = append(s.cases, selectCase{future: future, futureFunc: &f})
	return s
}

func (s *selectorImpl) AddDefault(f func()) {
	s.defaultFunc = &f
}

func (s *selectorImpl) Select(ctx Context) {
	state := getState(ctx)
	for {
		for _, pair := range s.cases {
			if pair.receiveFunc != nil {
				v, ok, more := pair.channel.ReceiveAsyncWithMoreFlag()
				if ok || !more {
					f := *pair.receiveFunc
					f(v)
					state.unblocked()
					return
				}
			} else if pair.receiveWithMoreFlagFunc != nil {
				v, ok, more := pair.channel.ReceiveAsyncWithMoreFlag()
				if ok || !more {
					f := *pair.receiveWithMoreFlagFunc
					f(v, more)
					state.unblocked()
					return
				}
			} else if pair.sendFunc != nil {
				ok := pair.channel.SendAsync(*pair.sendValue)
				if ok {
					f := *pair.sendFunc
					f()
					state.unblocked()
					return
				}
			} else if pair.futureFunc != nil {
				if pair.future.IsReady() {
					f := *pair.futureFunc
					f(pair.future.Get(ctx))
					state.unblocked()
					return
				}
			} else {
				panic("Invalid case")
			}
		}
		if s.defaultFunc != nil {
			f := *s.defaultFunc
			f()
			state.unblocked()
			return
		}
		state.yield(fmt.Sprintf("blocked on %s.Select", s.name))
	}
}

// NewWorkflowDefinition creates a  WorkflowDefinition from a Workflow
func NewWorkflowDefinition(workflow Workflow) workflowDefinition {
	return &syncWorkflowDefinition{workflow: workflow}
}
