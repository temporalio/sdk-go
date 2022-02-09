// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/converter"
)

func createRootTestContext() (interceptor *workflowEnvironmentInterceptor, ctx Context) {
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	envInterceptor, ctx, err := newWorkflowContext(env.impl, env.impl.GetRegistry().interceptors)
	if err != nil {
		panic(err)
	}
	return envInterceptor, ctx
}

func createNewDispatcher(f func(ctx Context)) dispatcher {
	interceptor, ctx := createRootTestContext()
	result, _ := newDispatcher(ctx, interceptor, f)
	result.interceptor = interceptor
	return result
}

func requireNoExecuteErr(t *testing.T, err error) {
	if err != nil {
		require.IsType(t, (*workflowPanicError)(nil), err)
		require.NoError(t, err, err.(*workflowPanicError).StackTrace())
	}
}

func TestDispatcher(t *testing.T) {
	value := "foo"
	d := createNewDispatcher(func(ctx Context) { value = "bar" })
	defer d.Close()
	require.Equal(t, "foo", value)
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
	require.Equal(t, "bar", value)
}

func TestNonBlockingChildren(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		for i := 0; i < 10; i++ {
			ii := i
			Go(ctx, func(ctx Context) {
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	require.EqualValues(t, 11, len(history))
}

func TestNonbufferedChannel(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			var v string
			more := c1.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child-end-%v", v))
		})
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"root-before-channel-put",
		"child-start",
		"child-end-value1",
		"root-after-channel-put",
	}
	require.EqualValues(t, expected, history)

}

func TestNonbufferedChannelBlockedReceive(t *testing.T) {
	var history []string
	var c2 Channel
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 = NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			var v string
			more := c1.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child1-end1-%v", v))
			more = c1.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child1-end2-%v", v))
		})
		Go(ctx, func(ctx Context) {
			var v string
			history = append(history, "child2-start")
			more := c2.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child2-end1-%v", v))
			more = c2.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child2-end2-%v", v))
		})

		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value11")
		c1.Send(ctx, "value12")
		history = append(history, "root-after-channel-put")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	c2.SendAsync("value21")
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	_ = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	c2.SendAsync("value22")
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	require.True(t, d.IsDone(), d.StackTrace())
}

func TestBufferedChannelPut(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewBufferedChannel(ctx, 1)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			var v1, v2 string
			more := c1.Receive(ctx, &v1)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child-end-%v", v1))
			c1.Receive(ctx, &v2)
			history = append(history, fmt.Sprintf("child-end-%v", v2))

		})
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put1")
		c1.Send(ctx, "value2")
		history = append(history, "root-after-channel-put2")
	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"root-before-channel-put",
		"root-after-channel-put1",
		"child-start",
		"child-end-value1",
		"child-end-value2",
		"root-after-channel-put2",
	}
	require.EqualValues(t, expected, history)
}

func TestBufferedChannelGet(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewBufferedChannel(ctx, 2)

		Go(ctx, func(ctx Context) {
			history = append(history, "child1-start")
			c2.Send(ctx, "bar1")
			history = append(history, "child1-get")
			var v1 string
			more := c1.Receive(ctx, &v1)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child1-end-%v", v1))

		})
		Go(ctx, func(ctx Context) {
			history = append(history, "child2-start")
			c2.Send(ctx, "bar2")
			history = append(history, "child2-get")
			var v1 string
			more := c1.Receive(ctx, &v1)
			require.True(t, more)
			history = append(history, fmt.Sprintf("child2-end-%v", v1))
		})
		history = append(history, "root-before-channel-get1")
		c2.Receive(ctx, nil)
		history = append(history, "root-before-channel-get2")
		c2.Receive(ctx, nil)
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put1")
		c1.Send(ctx, "value2")
		history = append(history, "root-after-channel-put2")
	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n")+"\n\n"+d.StackTrace())

	expected := []string{
		"root-before-channel-get1",
		"child1-start",
		"child1-get",
		"child2-start",
		"child2-get",
		"root-before-channel-get2",
		"root-before-channel-put",
		"root-after-channel-put1",
		"root-after-channel-put2",
		"child1-end-value1",
		"child2-end-value2",
	}
	require.EqualValues(t, expected, history)
}

func TestNotBlockingSelect(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewBufferedChannel(ctx, 1)
		c2 := NewBufferedChannel(ctx, 1)
		s := NewSelector(ctx)
		s.
			AddReceive(c1, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddReceive(c2, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c2-%v", v))
			}).
			AddDefault(func() { history = append(history, "default") })
		require.False(t, s.HasPending())
		c1.Send(ctx, "one")
		require.True(t, s.HasPending())
		s.Select(ctx)
		require.False(t, s.HasPending())
		c2.Send(ctx, "two")
		require.True(t, s.HasPending())
		s.Select(ctx)
		require.False(t, s.HasPending())
		s.Select(ctx)
		require.False(t, s.HasPending())
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"c1-one",
		"c2-two",
		"default",
	}
	require.EqualValues(t, expected, history)
}

func TestBlockingSelect(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "add-one")
			c1.Send(ctx, "one")
			history = append(history, "add-one-done")

		})
		Go(ctx, func(ctx Context) {
			history = append(history, "add-two")
			c2.Send(ctx, "two")
			history = append(history, "add-two-done")
		})

		s := NewSelector(ctx)
		s.
			AddReceive(c1, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddReceive(c2, func(c ReceiveChannel, more bool) {
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c2-%v", v))
			})
		history = append(history, "select1")
		assert.False(t, s.HasPending())
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n"))

	expected := []string{
		"select1",
		"add-one",
		"add-one-done",
		"add-two",
		"c1-one",
		"select2",
		"c2-two",
		"done",
		"add-two-done",
	}
	require.EqualValues(t, expected, history)
}

func TestBlockingSelectAsyncSend(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {

		c1 := NewChannel(ctx)
		s := NewSelector(ctx)
		s.
			AddReceive(c1, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v int
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c1-%v", v))
			})
		for i := 0; i < 3; i++ {
			ii := i // to reference within closure
			Go(ctx, func(ctx Context) {
				history = append(history, fmt.Sprintf("add-%v", ii))
				c1.SendAsync(ii)
			})
			history = append(history, fmt.Sprintf("select-%v", ii))
			s.Select(ctx)
		}
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n"))

	expected := []string{
		"select-0",
		"add-0",
		"c1-0",
		"select-1",
		"add-1",
		"c1-1",
		"select-2",
		"add-2",
		"c1-2",
		"done",
	}
	require.EqualValues(t, expected, history)
}

func TestSelectOnClosedChannel(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c := NewBufferedChannel(ctx, 1)
		c.Send(ctx, 5)
		c.Close()

		selector := NewNamedSelector(ctx, "waiting for channel")

		selector.AddReceive(c, func(f ReceiveChannel, more bool) {
			var n int

			if !more {
				history = append(history, "more from function is false")
				return
			}

			more = f.Receive(ctx, &n)
			if !more {
				history = append(history, "more from receive is false")
				return
			}
			history = append(history, fmt.Sprintf("got message on channel: %v", n))
		})
		require.True(t, selector.HasPending())
		selector.Select(ctx)
		require.True(t, selector.HasPending())
		selector.Select(ctx)
		require.False(t, selector.HasPending())
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n"))

	expected := []string{
		"got message on channel: 5",
		"more from function is false",
	}
	require.EqualValues(t, expected, history)
}

func TestBlockingSelectAsyncSend2(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewBufferedChannel(ctx, 100)
		c2 := NewBufferedChannel(ctx, 100)
		s := NewSelector(ctx)
		s.
			AddReceive(c1, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddReceive(c2, func(c ReceiveChannel, more bool) {
				require.True(t, more)
				var v string
				c.Receive(ctx, &v)
				history = append(history, fmt.Sprintf("c2-%v", v))
			})
		require.False(t, s.HasPending())
		history = append(history, "send-s2")
		c2.SendAsync("s2")
		require.True(t, s.HasPending())
		history = append(history, "select-0")
		s.Select(ctx)
		require.False(t, s.HasPending())
		history = append(history, "send-s1")
		c1.SendAsync("s1")
		require.True(t, s.HasPending())
		history = append(history, "select-1")
		s.Select(ctx)
		require.False(t, s.HasPending())
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n"))

	expected := []string{
		"send-s2",
		"select-0",
		"c2-s2",
		"send-s1",
		"select-1",
		"c1-s1",
		"done",
	}
	require.EqualValues(t, expected, history)
}

func TestSendSelect(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "receiver")
			var v string
			more := c2.Receive(ctx, &v)
			require.True(t, more)
			history = append(history, fmt.Sprintf("c2-%v", v))
			more = c1.Receive(ctx, &v)

			require.True(t, more)
			history = append(history, fmt.Sprintf("c1-%v", v))
		})
		s := NewSelector(ctx)
		require.False(t, s.HasPending())
		s.AddSend(c1, "one", func() { history = append(history, "send1") }).
			AddSend(c2, "two", func() { history = append(history, "send2") })
		history = append(history, "select1")
		s.Select(ctx)
		require.True(t, s.HasPending())
		history = append(history, "select2")
		s.Select(ctx)
		require.False(t, s.HasPending())
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"select1",
		"receiver",
		"c2-two",
		"send2",
		"select2",
		"send1",
		"done",
		"c1-one",
	}
	require.EqualValues(t, expected, history)
}

func TestSendSelectWithAsyncReceive(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "receiver")
			var v string
			ok, more := c2.ReceiveAsyncWithMoreFlag(&v)
			require.True(t, ok)
			require.True(t, more)
			history = append(history, fmt.Sprintf("c2-%v", v))
			more = c1.Receive(ctx, &v)

			require.True(t, more)
			history = append(history, fmt.Sprintf("c1-%v", v))
		})
		s := NewSelector(ctx)
		s.AddSend(c1, "one", func() { history = append(history, "send1") }).
			AddSend(c2, "two", func() { history = append(history, "send2") })
		history = append(history, "select1")
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), strings.Join(history, "\n"))

	expected := []string{
		"select1",
		"receiver",
		"c2-two",
		"send2",
		"select2",
		"send1",
		"done",
		"c1-one",
	}
	require.EqualValues(t, expected, history)
}

func TestChannelClose(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		jobs := NewBufferedChannel(ctx, 5)
		done := NewNamedChannel(ctx, "done")

		GoNamed(ctx, "receiver", func(ctx Context) {
			for {
				var j int
				more := jobs.Receive(ctx, &j)
				if more {
					history = append(history, fmt.Sprintf("received job %v", j))
				} else {
					history = append(history, "received all jobs")
					done.Send(ctx, true)
					return
				}
			}
		})
		for j := 1; j <= 3; j++ {
			jobs.Send(ctx, j)
			history = append(history, fmt.Sprintf("sent job %v", j))
		}
		jobs.Close()
		history = append(history, "sent all jobs")
		done.Receive(ctx, nil)
		history = append(history, "done")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone(), d.StackTrace())

	expected := []string{
		"sent job 1",
		"sent job 2",
		"sent job 3",
		"sent all jobs",
		"received job 1",
		"received job 2",
		"received job 3",
		"received all jobs",
		"done",
	}
	require.EqualValues(t, expected, history)
}

func TestSendClosedChannel(t *testing.T) {
	d := createNewDispatcher(func(ctx Context) {
		defer func() {
			require.NotNil(t, recover(), "panic expected")
		}()
		c := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			c.Close()
		})
		c.Send(ctx, "baz")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
}

func TestBlockedSendClosedChannel(t *testing.T) {
	d := createNewDispatcher(func(ctx Context) {
		defer func() {
			require.NotNil(t, recover(), "panic expected")
		}()
		c := NewBufferedChannel(ctx, 5)
		c.Send(ctx, "bar")
		c.Close()
		c.Send(ctx, "baz")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
}

func TestAsyncSendClosedChannel(t *testing.T) {
	d := createNewDispatcher(func(ctx Context) {
		defer func() {
			require.NotNil(t, recover(), "panic expected")
		}()
		c := NewBufferedChannel(ctx, 5)
		c.Send(ctx, "bar")
		c.Close()
		_ = c.SendAsync("baz")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
}

func TestDispatchClose(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c := NewNamedChannel(ctx, "forever_blocked")
		for i := 0; i < 10; i++ {
			ii := i
			GoNamed(ctx, fmt.Sprintf("c-%v", i), func(ctx Context) {
				c.Receive(ctx, nil) // blocked forever
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
		c.Receive(ctx, nil) // blocked forever
	})
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.False(t, d.IsDone())
	stack := d.StackTrace()
	// 11 coroutines (3 lines each) + 10 nl
	require.EqualValues(t, 11*3+10, len(strings.Split(stack, "\n")), stack)
	require.Contains(t, stack, "coroutine root [blocked on forever_blocked.Receive]:")
	for i := 0; i < 10; i++ {
		require.Contains(t, stack, fmt.Sprintf("coroutine c-%v [blocked on forever_blocked.Receive]:", i))
	}
	beforeClose := runtime.NumGoroutine()
	d.Close()
	time.Sleep(100 * time.Millisecond) // Let all goroutines to die
	closedCount := beforeClose - runtime.NumGoroutine()
	require.True(t, closedCount >= 11)
	expected := []string{
		"root",
	}
	require.EqualValues(t, expected, history)
}

func TestPanic(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		c := NewNamedChannel(ctx, "forever_blocked")
		for i := 0; i < 10; i++ {
			ii := i
			GoNamed(ctx, fmt.Sprintf("c-%v", i), func(ctx Context) {
				if ii == 9 {
					panic("simulated failure")
				}
				c.Receive(ctx, nil) // blocked forever
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
		c.Receive(ctx, nil) // blocked forever
	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.Error(t, err)
	value := err.Error()
	require.EqualValues(t, "simulated failure", value)
	require.EqualValues(t, "simulated failure", err.Error())
	panicError, ok := err.(*workflowPanicError)
	require.True(t, ok)
	require.Contains(t, panicError.StackTrace(), "go.temporal.io/sdk/internal.TestPanic")
}

func TestChannelReceivePointer(t *testing.T) {
	// This confirms that a sent pointer can be received as a pointer
	d := createNewDispatcher(func(ctx Context) {
		type MyStruct struct{ Foo string }
		// Create channel and a non-pointer and a pointer in
		c := NewBufferedChannel(ctx, 2)
		c.Send(ctx, MyStruct{Foo: "1"})
		c.Send(ctx, &MyStruct{Foo: "2"})

		// Confirm they both can be received as pointers
		var val MyStruct
		c.Receive(ctx, &val)
		require.Equal(t, "1", val.Foo)
		c.Receive(ctx, &val)
		require.Equal(t, "2", val.Foo)
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
}

func TestAwait(t *testing.T) {
	flag := false
	d := createNewDispatcher(func(ctx Context) {
		_ = Await(ctx, func() bool { return flag })
	})
	defer d.Close()
	err := d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	flag = true
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.True(t, d.IsDone())
}

func TestDeadlockDetectorAndAwaitRace(t *testing.T) {
	// Expecting deadlock detection timeout instead of a data race.
	defer func() {
		err := recover()
		require.NotNil(t, err, "panic expected")
		require.Equal(t, err, "Potential deadlock detected: workflow goroutine \"root\" didn't yield for over a second")
	}()
	d := createNewDispatcher(func(ctx Context) {
		_ = Await(ctx, func() bool {
			time.Sleep(defaultDeadlockDetectionTimeout)
			return false
		})
	})
	_ = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	d.Close()
}

func TestAwaitCancellation(t *testing.T) {
	var awaitError error
	interceptor, ctx := createRootTestContext()
	ctx, cancelHandler := WithCancel(ctx)
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		awaitError = Await(ctx, func() bool { return false })
	})
	defer d.Close()
	err := d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	cancelHandler()
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.True(t, d.IsDone())
	require.Error(t, awaitError)
	_, ok := awaitError.(*CanceledError)
	require.True(t, ok)
}

func TestAwaitWithTimeoutNoTimeout(t *testing.T) {
	var awaitWithTimeoutError error
	flag := false
	var awaitOk bool
	d := createNewDispatcher(func(ctx Context) {
		awaitOk, awaitWithTimeoutError = AwaitWithTimeout(ctx, time.Hour, func() bool { return flag })
	})
	defer d.Close()
	err := d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	require.False(t, awaitOk)
	require.NoError(t, awaitWithTimeoutError)
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	flag = true
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.True(t, awaitOk)
	require.True(t, d.IsDone())
}

func TestAwaitWithTimeoutCancellation(t *testing.T) {
	var awaitWithTimeoutError error
	var awaitOk bool
	interceptor, ctx := createRootTestContext()
	ctx, cancelHandler := WithCancel(ctx)
	d, _ := newDispatcher(ctx, interceptor, func(ctx Context) {
		awaitOk, awaitWithTimeoutError = AwaitWithTimeout(ctx, time.Hour, func() bool { return false })
	})
	defer d.Close()
	err := d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.False(t, d.IsDone())
	require.False(t, awaitOk)
	require.NoError(t, awaitWithTimeoutError)
	cancelHandler()
	err = d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout)
	require.NoError(t, err)
	require.True(t, d.IsDone())
	require.Error(t, awaitWithTimeoutError)
	_, ok := awaitWithTimeoutError.(*CanceledError)
	require.True(t, ok)
}

func TestFutureSetValue(t *testing.T) {
	var history []string
	var f Future
	var s Settable
	d := createNewDispatcher(func(ctx Context) {
		f, s = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			require.False(t, f.IsReady())
			var v string
			err := f.Get(ctx, &v)
			require.NoError(t, err)
			require.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v", v))
			// test second get of the ready future
			err = f.Get(ctx, &v)
			require.NoError(t, err)
			require.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v", v))
		})
		history = append(history, "root-end")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	require.False(t, f.IsReady())
	s.SetValue("value1")
	require.True(t, f.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"future-set",
		"future-get-value1",
		"child-end-value1",
	}
	require.EqualValues(t, expected, history)

}

func TestFutureFail(t *testing.T) {
	var history []string
	var f Future
	var s Settable
	d := createNewDispatcher(func(ctx Context) {
		f, s = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			require.False(t, f.IsReady())
			var v string
			err := f.Get(ctx, &v)
			require.Error(t, err)
			require.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v", err))
			// test second get of the ready future
			err = f.Get(ctx, &v)
			require.Error(t, err)
			require.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v", err))
		})
		history = append(history, "root-end")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	require.False(t, f.IsReady())
	s.SetError(errors.New("value1"))
	assert.True(t, f.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"future-set",
		"future-get-value1",
		"child-end-value1",
	}
	require.EqualValues(t, expected, history)
}

func TestFutureSet(t *testing.T) {
	var history []string
	var f1, f2 Future
	var s1, s2 Settable
	d := createNewDispatcher(func(ctx Context) {
		f1, s1 = NewFuture(ctx)
		f2, s2 = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			require.False(t, f1.IsReady())
			var v string
			err := f1.Get(ctx, &v)
			require.Error(t, err)
			require.NotNil(t, v)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get-%v-%v", v, err))
			// test second get of the ready future
			err = f1.Get(ctx, &v)
			require.Error(t, err)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get2-%v-%v", v, err))

			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.True(t, f2.IsReady())
			history = append(history, fmt.Sprintf("f2-get-%v-%v", v, err))

			// test second get of the ready future
			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f2-get2-%v-%v", v, err))

			history = append(history, "child-end")
		})
		history = append(history, "root-end")
	})
	defer d.Close()

	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f1-set")
	require.False(t, f1.IsReady())
	s1.Set("value-will-be-ignored", errors.New("error1"))
	assert.True(t, f1.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f2-set")
	require.False(t, f2.IsReady())
	s2.Set("value2", nil)
	assert.True(t, f2.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"f1-set",
		"f1-get--error1",
		"f1-get2--error1",
		"f2-set",
		"f2-get-value2-<nil>",
		"f2-get2-value2-<nil>",
		"child-end",
	}
	require.EqualValues(t, expected, history)
}

func TestFutureChain(t *testing.T) {
	var history []string
	var f1, cf1, f2, cf2 Future
	var s1, cs1, s2, cs2 Settable

	d := createNewDispatcher(func(ctx Context) {
		f1, s1 = NewFuture(ctx)
		cf1, cs1 = NewFuture(ctx)
		s1.Chain(cf1)
		f2, s2 = NewFuture(ctx)
		cf2, cs2 = NewFuture(ctx)
		s2.Chain(cf2)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			require.False(t, f1.IsReady())
			var v string
			err := f1.Get(ctx, &v)
			require.Error(t, err)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get-%v-%v", v, err))
			// test second get of the ready future
			err = f1.Get(ctx, &v)
			require.Error(t, err)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get2-%v-%v", v, err))

			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.Equal(t, "value2", v)
			require.True(t, f2.IsReady())
			history = append(history, fmt.Sprintf("f2-get-%v-%v", v, err))
			// test second get of the ready future
			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.Equal(t, "value2", v)
			require.True(t, f2.IsReady())
			history = append(history, fmt.Sprintf("f2-get2-%v-%v", v, err))
		})
		history = append(history, "root-end")

	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f1-set")
	require.False(t, f1.IsReady())
	cs1.Set("value1-will-be-ignored", errors.New("error1"))
	assert.True(t, f1.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f2-set")
	require.False(t, f2.IsReady())
	cs2.Set("value2", nil)
	assert.True(t, f2.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"f1-set",
		"f1-get--error1",
		"f1-get2--error1",
		"f2-set",
		"f2-get-value2-<nil>",
		"f2-get2-value2-<nil>",
	}
	require.EqualValues(t, expected, history)
}

func TestSelectFuture(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		future1, settable1 := NewFuture(ctx)
		future2, settable2 := NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "add-one")
			settable1.SetValue("one")
		})
		Go(ctx, func(ctx Context) {
			history = append(history, "add-two")
			settable2.SetValue("two")
		})

		s := NewSelector(ctx)
		s.
			AddFuture(future1, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddFuture(future2, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c2-%v", v))
			})
		history = append(history, "select1")
		require.False(t, s.HasPending())
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"select1",
		"add-one",
		"add-two",
		"c1-one",
		"select2",
		"c2-two",
		"done",
	}
	require.EqualValues(t, expected, history)
}

func TestSelectDecodeFuture(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		future1, settable1 := newDecodeFuture(ctx, "testFn1")
		future2, settable2 := newDecodeFuture(ctx, "testFn2")
		Go(ctx, func(ctx Context) {
			history = append(history, "add-one")
			v, err := converter.GetDefaultDataConverter().ToPayloads([]byte("one"))
			require.NoError(t, err)
			settable1.SetValue(v)
		})
		Go(ctx, func(ctx Context) {
			history = append(history, "add-two")
			v, err := converter.GetDefaultDataConverter().ToPayloads("two")
			require.NoError(t, err)
			settable2.SetValue(v)
		})

		s := NewSelector(ctx)
		s.
			AddFuture(future1, func(f Future) {
				var v []byte
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c1-%s", v))
			}).
			AddFuture(future2, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c2-%s", v))
			})
		history = append(history, "select1")
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"select1",
		"add-one",
		"add-two",
		"c1-one",
		"select2",
		"c2-two",
		"done",
	}
	require.EqualValues(t, expected, history)
}

func TestDecodeFutureChain(t *testing.T) {
	var history []string
	var f1, cf1, f2, cf2 Future
	var s1, cs1, s2, cs2 Settable

	d := createNewDispatcher(func(ctx Context) {
		f1, s1 = newDecodeFuture(ctx, "testFn")
		cf1, cs1 = newDecodeFuture(ctx, "testFun")
		f2, s2 = newDecodeFuture(ctx, "testFn")
		cf2, cs2 = newDecodeFuture(ctx, "testFun")
		s1.Chain(cf1)
		s2.Chain(cf2)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			require.False(t, f1.IsReady())
			var v []byte
			err := f1.Get(ctx, &v)
			require.Error(t, err)
			require.Nil(t, v)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get-%s-%v", v, err))
			// test second get of the ready future
			err = f1.Get(ctx, &v)
			require.Error(t, err)
			require.Nil(t, v)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f1-get2-%s-%v", v, err))

			// for f2
			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.NotNil(t, v)
			require.True(t, f1.IsReady())
			history = append(history, fmt.Sprintf("f2-get-%s-%v", v, err))
			// test second get of the ready future
			err = f2.Get(ctx, &v)
			require.NoError(t, err)
			require.NotNil(t, v)
			require.True(t, f2.IsReady())
			history = append(history, fmt.Sprintf("f2-get2-%s-%v", v, err))
		})
		history = append(history, "root-end")
	})
	defer d.Close()
	require.EqualValues(t, 0, len(history))
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	// set f1
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f1-set")
	require.False(t, f1.IsReady())
	cs1.Set([]byte("value-will-be-ignored"), errors.New("error1"))
	assert.True(t, f1.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	// set f2
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "f2-set")
	require.False(t, f2.IsReady())
	v2, err := converter.GetDefaultDataConverter().ToPayloads([]byte("value2"))
	require.NoError(t, err)
	cs2.Set(v2, nil)
	assert.True(t, f2.IsReady())
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))

	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"f1-set",
		"f1-get--error1",
		"f1-get2--error1",
		"f2-set",
		"f2-get-value2-<nil>",
		"f2-get2-value2-<nil>",
	}
	require.EqualValues(t, expected, history)
}

func TestSelectFuture_WithBatchSets(t *testing.T) {
	var history []string
	d := createNewDispatcher(func(ctx Context) {
		future1, settable1 := NewFuture(ctx)
		future2, settable2 := NewFuture(ctx)
		future3, settable3 := NewFuture(ctx)

		s := NewSelector(ctx)
		s.
			AddFuture(future1, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddFuture(future2, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c2-%v", v))
			}).
			AddFuture(future3, func(f Future) {
				var v string
				err := f.Get(ctx, &v)
				require.NoError(t, err)
				history = append(history, fmt.Sprintf("c3-%v", v))
			})

		require.False(t, s.HasPending())
		settable2.Set("two", nil)
		require.True(t, s.HasPending())
		s.Select(ctx)
		require.False(t, s.HasPending())
		settable3.Set("three", nil)
		require.True(t, s.HasPending())
		settable1.Set("one", nil)
		require.True(t, s.HasPending())
		s.Select(ctx)
		require.True(t, s.HasPending())
		s.Select(ctx)
		require.False(t, s.HasPending())
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())

	expected := []string{
		"c2-two",
		"c1-one",
		"c3-three",
	}
	require.EqualValues(t, expected, history)
}

func TestChainedFuture(t *testing.T) {
	activityFn := func(arg int) (int, error) {
		return arg, nil
	}
	workflowFn := func(ctx Context) (out int, err error) {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			ScheduleToCloseTimeout: time.Minute,
		})
		f := ExecuteActivity(ctx, activityFn, 5)
		fut, set := NewFuture(ctx)
		set.Chain(f)
		err = fut.Get(ctx, &out)
		return
	}

	s := WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.RegisterActivity(activityFn)

	env.ExecuteWorkflow(workflowFn)
	err := env.GetWorkflowError()
	require.NoError(t, err)
	var out int
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, 5, out)
}

func TestFutureUnmarshalPointerToPointer(t *testing.T) {
	// Standard futures and decode futures should both be able to unmarshal into
	// a pointer even if they already are a pointer.
	d := createNewDispatcher(func(ctx Context) {
		type MyStruct struct{ Value string }
		toSet := &MyStruct{Value: "MyValue"}
		toSetPayload, err := converter.GetDefaultDataConverter().ToPayloads(toSet)
		require.NoError(t, err)
		var toGet1, toGet2 MyStruct

		fut1, set1 := newDecodeFuture(ctx, nil)
		set1.SetValue(toSetPayload)
		require.NoError(t, fut1.Get(ctx, &toGet1))
		require.Equal(t, "MyValue", toGet1.Value)

		fut2, set2 := NewFuture(ctx)
		set2.SetValue(toSet)
		require.NoError(t, fut2.Get(ctx, &toGet2))
		require.Equal(t, "MyValue", toGet2.Value)
	})
	defer d.Close()
	requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
	require.True(t, d.IsDone())
}

// Mostly from https://github.com/uber-go/cadence-client/pull/1124
func TestContextCancelRace(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wf := func(ctx Context) error {
		ctx, cancel := WithCancel(ctx)
		racyCancel := func(ctx Context) {
			defer cancel() // defer is necessary as Sleep will never return due to Goexit
			_ = Sleep(ctx, time.Hour)
		}
		// start a handful to increase odds of a race being detected
		for i := 0; i < 10; i++ {
			Go(ctx, racyCancel)
		}

		_ = Sleep(ctx, time.Minute) // die early
		return nil
	}
	env.RegisterWorkflow(wf)
	env.ExecuteWorkflow(wf)
	assert.NoError(t, env.GetWorkflowError())
}

// Mostly from https://github.com/uber-go/cadence-client/pull/1141
func TestContextChildCancelRace(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wf := func(ctx Context) error {
		ctx, cancel := WithCancel(ctx)
		racyCancel := func(ctx Context) {
			defer cancel() // defer is necessary as Sleep will never return due to Goexit
			defer func() {
				_, ccancel := WithCancel(ctx)
				cancel()
				ccancel()
			}()
			_ = Sleep(ctx, time.Hour)
		}
		// start a handful to increase odds of a race being detected
		for i := 0; i < 10; i++ {
			Go(ctx, racyCancel)
		}

		_ = Sleep(ctx, time.Minute) // die early
		return nil
	}
	env.RegisterWorkflow(wf)
	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
}
