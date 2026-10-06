package internal

import (
	"fmt"
	"sync"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// Context is a clone of context.Context with Done() returning Channel instead
// of native channel.
// A Context carries a deadline, a cancellation signal, and other values across
// API boundaries.
//
// Context's methods may be called by multiple goroutines simultaneously.
//
// Exposed as: [go.temporal.io/sdk/workflow.Context]
type Context = workflowcontext.Context

// An emptyCtx is never canceled, has no values, and has no deadline.  It is not
// struct{}, since vars of this type must have distinct addresses.
type emptyCtx int

func (*emptyCtx) Deadline() (deadline time.Time, ok bool) {
	return
}

func (*emptyCtx) Done() Channel {
	return nil
}

func (*emptyCtx) Err() error {
	return nil
}

func (*emptyCtx) Value(_ any) any {
	return nil
}

func (e *emptyCtx) String() string {
	switch e {
	case background:
		return "context.Background"
	case todo:
		return "context.TODO"
	}
	return "unknown empty Context"
}

var (
	background = new(emptyCtx)
	todo       = new(emptyCtx)
)

// Background returns a non-nil, empty Context. It is never canceled, has no
// values, and has no deadline
func Background() Context {
	return background
}

// ErrCanceled is the error returned by Context.Err when the context is canceled.
//
// Exposed as: [go.temporal.io/sdk/workflow.ErrCanceled]
var ErrCanceled = NewCanceledError()

// ErrDeadlineExceeded is the error returned by Context.Err when the context's
// deadline passes.
//
// Exposed as: [go.temporal.io/sdk/workflow.ErrDeadlineExceeded]
var ErrDeadlineExceeded = NewTimeoutError("deadline exceeded", enumspb.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE, nil)

// A CancelFunc tells an operation to abandon its work.
// A CancelFunc does not wait for the work to stop.
// After the first call, subsequent calls to a CancelFunc do nothing.
//
// Exposed as: [go.temporal.io/sdk/workflow.CancelFunc]
type CancelFunc func()

// WithCancel returns a copy of parent with a new Done channel. The returned
// context's Done channel is closed when the returned cancel function is called
// or when the parent context's Done channel is closed, whichever happens first.
//
// Canceling this context releases resources associated with it, so code should
// call cancel as soon as the operations running in this Context complete.
//
// Exposed as: [go.temporal.io/sdk/workflow.WithCancel]
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	c := newCancelCtx(parent)
	propagateCancel(parent, c)
	return c, func() { c.cancel(true, ErrCanceled) }
}

// NewDisconnectedContext returns a new context that won't propagate parent's cancellation to the new child context.
// One common use case is to do cleanup work after workflow is canceled.
//
//	err := workflow.ExecuteActivity(ctx, ActivityFoo).Get(ctx, &activityFooResult)
//	if err != nil && temporal.IsCanceledError(ctx.Err()) {
//	  // activity failed, and workflow context is canceled
//	  disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx);
//	  workflow.ExecuteActivity(disconnectedCtx, handleCancellationActivity).Get(disconnectedCtx, nil)
//	  return err // workflow return CanceledError
//	}
//
// Exposed as: [go.temporal.io/sdk/workflow.NewDisconnectedContext]
func NewDisconnectedContext(parent Context) (ctx Context, cancel CancelFunc) {
	c := newCancelCtx(parent)
	return c, func() { c.cancel(true, ErrCanceled) }
}

// newCancelCtx returns an initialized cancelCtx.
func newCancelCtx(parent Context) *cancelCtx {
	return &cancelCtx{
		Context: parent,
		done:    NewNamedChannel(parent, "cancelCtx-done-channel"),
	}
}

// propagateCancel arranges for child to be canceled when parent is.
func propagateCancel(parent Context, child canceler) {
	if parent.Done() == nil {
		return // parent is never canceled
	}
	if p, ok := parentCancelCtx(parent); ok {
		if parentErr := p.Err(); parentErr != nil {
			// parent has already been canceled
			child.cancel(false, parentErr)
		} else {
			p.childrenLock.Lock()
			p.children.add(child)
			p.childrenLock.Unlock()
		}
	} else {
		panic("cancelCtx not found")
	}
}

// parentCancelCtx follows a chain of parent references until it finds a
// *cancelCtx.  This function understands how each of the concrete types in this
// package represents its parent.
func parentCancelCtx(parent Context) (*cancelCtx, bool) {
	for {
		switch c := parent.(type) {
		case *cancelCtx:
			return c, true
		case *valueCtx:
			parent = c.Context
		default:
			return nil, false
		}
	}
}

// removeChild removes a context from its parent.
func removeChild(parent Context, child canceler) {
	p, ok := parentCancelCtx(parent)
	if !ok {
		return
	}
	p.childrenLock.Lock()
	p.children.remove(child)
	p.childrenLock.Unlock()
}

// A canceler is a context type that can be canceled directly.  The
// implementations are *cancelCtx and *timerCtx.
type canceler interface {
	cancel(removeFromParent bool, err error)
	Done() Channel
}

// childNode preserves creation order while allowing the children map to find
// and unlink a child in constant time.
type childNode struct {
	child canceler
	prev  *childNode
	next  *childNode
}

type childList struct {
	nodes map[canceler]*childNode
	first *childNode
	last  *childNode
}

func (l *childList) add(child canceler) {
	if l.nodes == nil {
		l.nodes = make(map[canceler]*childNode)
	}
	if _, ok := l.nodes[child]; ok {
		return
	}

	node := &childNode{child: child, prev: l.last}
	if l.last == nil {
		l.first = node
	} else {
		l.last.next = node
	}
	l.last = node
	l.nodes[child] = node
}

func (l *childList) remove(child canceler) {
	node, ok := l.nodes[child]
	if !ok {
		return
	}

	delete(l.nodes, child)
	if node.prev == nil {
		l.first = node.next
	} else {
		node.prev.next = node.next
	}
	if node.next == nil {
		l.last = node.prev
	} else {
		node.next.prev = node.prev
	}

	node.prev = nil
	node.next = nil
}

// A cancelCtx can be canceled.  When canceled, it also cancels any children
// that implement canceler.
type cancelCtx struct {
	Context

	done Channel // closed by the first cancel call.

	// children stores cancelable child contexts by identity and creation order.
	// Legacy histories traverse the map; histories with [SDKFlagOrderedChildCancel]
	// traverse the list.
	children     childList
	childrenLock sync.Mutex
	err          error // set to non-nil by the first cancel call
	errLock      sync.RWMutex
}

func (c *cancelCtx) Done() Channel {
	return c.done
}

func (c *cancelCtx) Err() error {
	c.errLock.RLock()
	defer c.errLock.RUnlock()
	return c.err
}

func (c *cancelCtx) String() string {
	return fmt.Sprintf("%v.WithCancel", c.Context)
}

// cancel closes c.done, cancels each of c's children, and, if
// removeFromParent is true, removes c from its parent's children.
func (c *cancelCtx) cancel(removeFromParent bool, err error) {
	if err == nil {
		panic("context: internal error: missing cancel error")
	}
	// This can be called from separate goroutines concurrently, so we use the
	// presence of the error under lock to prevent duplicate calls
	c.errLock.Lock()
	alreadyCancelled := c.err != nil
	if !alreadyCancelled {
		c.err = err
	}
	c.errLock.Unlock()
	if alreadyCancelled {
		return
	}
	c.done.Close()
	c.childrenLock.Lock()
	children := c.children
	c.children = childList{}
	c.childrenLock.Unlock()
	// Avoid recording the SDK flag when cancellation order cannot affect behavior.
	if len(children.nodes) > 1 && GetWorkflowEnvironment(c).TryUse(SDKFlagOrderedChildCancel) {
		for node := children.first; node != nil; node = node.next {
			node.child.cancel(false, err)
		}
	} else {
		for child := range children.nodes {
			child.cancel(false, err)
		}
	}

	if removeFromParent {
		removeChild(c.Context, c)
	}
}

// WithValue returns a copy of parent in which the value associated with key is
// val.
//
// Use context Values only for request-scoped data that transits processes and
// APIs, not for passing optional parameters to functions.
//
// Exposed as: [go.temporal.io/sdk/workflow.WithValue]
func WithValue(parent Context, key any, val any) Context {
	return &valueCtx{parent, key, val}
}

// A valueCtx carries a key-value pair.  It implements Value for that key and
// delegates all other calls to the embedded Context.
type valueCtx struct {
	Context
	key, val any
}

func (c *valueCtx) String() string {
	return fmt.Sprintf("%v.WithValue(%#v, %#v)", c.Context, c.key, c.val)
}

func (c *valueCtx) Value(key any) any {
	if c.key == key {
		return c.val
	}
	return c.Context.Value(key)
}
