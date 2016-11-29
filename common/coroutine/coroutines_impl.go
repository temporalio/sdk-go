package coroutine

import (
	"golang.org/x/net/context"
)

type valueCallbackPair struct {
	value    interface{}
	callback func()
}

type channelImpl struct {
	size         int                 // Channel buffer size. 0 for non buffered.
	buffer       []interface{}       // buffered messages
	blockedSends []valueCallbackPair // puts waiting when buffer is full.
	blockedRecvs []func(interface{}) // receives waiting when no messages are available.
}

// Single case statement of the Select
type selectCase struct {
	channel   Channel       // Channel of this case.
	recvFunc  *RecvCaseFunc // function to call when channel has a message. nil for send case.
	sendFunc  *SendCaseFunc // function to call when channel accepted a message. nil for receive case.
	sendValue *interface{}  // value to send to the channel. Used only for send case.
}

// Implements Selector interface
type selectorImpl struct {
	cases       []selectCase     // cases that this select is comprised from
	defaultFunc *DefaultCaseFunc // default case
}

type contextImpl struct {
	context.Context
	dispatcher   *dispatcherImpl // dispatcher this context belongs to
	aboutToBlock chan bool       // Used to notify dispatcher that coroutine that owns this context is about to block
	unblock      chan bool       // Used to notify coroutine that it should continue executing
	keptBlocked  bool            // true indicates that coroutine didn't make any progress since the last yield unblocking
	closed       bool            // indicates that owning coroutine has finished execution
}

type dispatcherImpl struct {
	sequence   int
	coroutines []*contextImpl
}

// Assert that structs do indeed implement the interfaces
var _ Channel = (*channelImpl)(nil)
var _ Context = (*contextImpl)(nil)
var _ Selector = (*selectorImpl)(nil)
var _ Dispatcher = (*dispatcherImpl)(nil)

func (c *channelImpl) Recv(ctx Context) interface{} {
	ctxImpl := ctx.(*contextImpl)
	hasResult := false
	var result interface{}
	for {
		if hasResult {
			ctxImpl.unblocked()
			return result
		}
		if len(c.buffer) > 0 {
			r := c.buffer[0]
			c.buffer = c.buffer[1:]
			ctxImpl.unblocked()
			return r
		}
		if len(c.blockedSends) > 0 {
			b := c.blockedSends[0]
			c.blockedSends = c.blockedSends[1:]
			b.callback()
			ctxImpl.unblocked()
			return b.value
		}
		c.blockedRecvs = append(c.blockedRecvs, func(v interface{}) {
			result = v
			hasResult = true
		})
		ctxImpl.yield()
	}
}

func (c *channelImpl) RecvAsync(ctx Context) (v interface{}, ok bool) {
	ctxImpl := ctx.(*contextImpl)

	if len(c.buffer) > 0 {
		r := c.buffer[0]
		c.buffer = c.buffer[1:]
		ctxImpl.unblocked()
		return r, true
	}
	if len(c.blockedSends) > 0 {
		b := c.blockedSends[0]
		c.blockedSends = c.blockedSends[1:]
		b.callback()
		return b.value, true
	}
	return nil, false
}

func (c *channelImpl) Send(ctx Context, v interface{}) {
	ctxImpl := ctx.(*contextImpl)
	valueConsumed := false
	for {
		if valueConsumed {
			ctxImpl.unblocked()
			return
		}
		if len(c.buffer) < c.size {
			c.buffer = append(c.buffer, v)
			ctxImpl.unblocked()
			return
		}
		if len(c.blockedRecvs) > 0 {
			blockedGet := c.blockedRecvs[0]
			c.blockedRecvs = c.blockedRecvs[1:]
			blockedGet(v)
			ctxImpl.unblocked()
			return
		}
		c.blockedSends = append(c.blockedSends,
			valueCallbackPair{value: v, callback: func() { valueConsumed = true }})
		ctxImpl.yield()
	}
}

func (c *channelImpl) SendAsync(ctx Context, v interface{}) (ok bool) {
	if len(c.buffer) < c.size {
		c.buffer = append(c.buffer, v)
		return true
	}
	if len(c.blockedRecvs) > 0 {
		blockedGet := c.blockedRecvs[0]
		c.blockedRecvs = c.blockedRecvs[1:]
		blockedGet(v)
		return true
	}
	return false
}

// initialYield called at the beginning of the coroutine execution
func (ctx *contextImpl) initialYield() {
	<-ctx.unblock
}

// yield indicates that coroutine cannot make progress and should sleep
// this call blocks
func (ctx *contextImpl) yield() {
	ctx.aboutToBlock <- true
	<-ctx.unblock
	ctx.keptBlocked = true
}

// unblocked is called by coroutine to indicate that since the last time yield was unblocked channel or select
// where unblocked versus calling yield again after checking their condition
func (ctx *contextImpl) unblocked() {
	ctx.keptBlocked = false
}

func (ctx *contextImpl) call() {
	ctx.unblock <- true
	<-ctx.aboutToBlock
}

func (ctx *contextImpl) close() {
	ctx.closed = true
	ctx.aboutToBlock <- true
}

func (ctx *contextImpl) NewCoroutine(f Func) {
	ctx.dispatcher.newCoroutine(f)
}

func (ctx *contextImpl) NewSelector() Selector {
	return &selectorImpl{}
}

func (ctx *contextImpl) NewChannel() Channel {
	return &channelImpl{}
}

func (ctx *contextImpl) NewBufferedChannel(size int) Channel {
	return &channelImpl{size: size}
}

func (d *dispatcherImpl) newCoroutine(f Func) {
	ctx := d.newContext()
	go func(ctx *contextImpl) {
		defer ctx.close()
		ctx.initialYield()
		f(ctx)
	}(ctx)
}

func (d *dispatcherImpl) newContext() *contextImpl {
	c := &contextImpl{
		Context:      context.Background(),
		dispatcher:   d,
		aboutToBlock: make(chan bool, 1),
		unblock:      make(chan bool),
	}
	d.sequence++
	d.coroutines = append(d.coroutines, c)
	return c
}

func (d *dispatcherImpl) ExecuteUntilAllBlocked() {
	allBlocked := false
	// Keep executing until at least one goroutine made some progress
	for !allBlocked {
		// Give every coroutine chance to execute removing closed ones
		allBlocked = true
		lastSequence := d.sequence
		for i := 0; i < len(d.coroutines); i++ {
			c := d.coroutines[i]
			if !c.closed {
				c.call()
			}
			// c.call() can close the context so check again
			if c.closed {
				// remove the closed one from the slice
				d.coroutines = append(d.coroutines[:i],
					d.coroutines[i+1:]...)
				i--
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
}

func (d *dispatcherImpl) IsDone() bool {
	return len(d.coroutines) == 0
}

func (s *selectorImpl) AddRecv(c Channel, f RecvCaseFunc) Selector {
	s.cases = append(s.cases, selectCase{channel: c, recvFunc: &f})
	return s
}

func (s *selectorImpl) AddSend(c Channel, v interface{}, f SendCaseFunc) Selector {
	s.cases = append(s.cases, selectCase{channel: c, sendFunc: &f, sendValue: &v})
	return s
}

func (s *selectorImpl) AddDefault(f DefaultCaseFunc) {
	s.defaultFunc = &f
}

func (s *selectorImpl) Select(ctx Context) {
	ctxImpl := ctx.(*contextImpl)
	for {
		for _, pair := range s.cases {
			if pair.recvFunc != nil {
				v, ok := pair.channel.RecvAsync(ctx)
				if ok {
					f := *pair.recvFunc
					f(v)
					ctxImpl.unblocked()
					return
				}
			} else {
				ok := pair.channel.SendAsync(ctx, *pair.sendValue)
				if ok {
					f := *pair.sendFunc
					f()
					ctxImpl.unblocked()
					return
				}
			}
		}
		if s.defaultFunc != nil {
			f := *s.defaultFunc
			f()
			ctxImpl.unblocked()
			return
		}
		ctxImpl.yield()
	}
}
