package cadence

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatcher(t *testing.T) {
	value := "foo"
	d := newDispatcher(background, func(ctx Context) { value = "bar" })
	require.Equal(t, "foo", value)
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())
	require.Equal(t, "bar", value)
}

func TestNonBlockingChildren(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		for i := 0; i < 10; i++ {
			ii := i
			Go(ctx, func(ctx Context) {
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())

	require.EqualValues(t, 11, len(history))
}

func TestNonbufferedChannel(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			v, more := c1.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("child-end-%v", v))
		})
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put")

	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())

	expected := []string{
		"root-before-channel-put",
		"child-start",
		"child-end-value1",
		"root-after-channel-put",
	}
	require.EqualValues(t, expected, history)

}

func TestBufferedChannelPut(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewBufferedChannel(ctx, 1)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			v1, more := c1.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("child-end-%v", v1))
			v2, more := c1.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("child-end-%v", v2))

		})
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put1")
		c1.Send(ctx, "value2")
		history = append(history, "root-after-channel-put2")
	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
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
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewBufferedChannel(ctx, 2)

		Go(ctx, func(ctx Context) {
			history = append(history, "child1-start")
			c2.Send(ctx, "bar1")
			history = append(history, "child1-get")
			v1, more := c1.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("child1-end-%v", v1))

		})
		Go(ctx, func(ctx Context) {
			history = append(history, "child2-start")
			c2.Send(ctx, "bar2")
			history = append(history, "child2-get")
			v1, more := c1.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("child2-end-%v", v1))
		})
		history = append(history, "root-before-channel-get1")
		c2.Recv(ctx)
		history = append(history, "root-before-channel-get2")
		c2.Recv(ctx)
		history = append(history, "root-before-channel-put")
		c1.Send(ctx, "value1")
		history = append(history, "root-after-channel-put1")
		c1.Send(ctx, "value2")
		history = append(history, "root-after-channel-put2")
	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())

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
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewBufferedChannel(ctx, 1)
		c2 := NewBufferedChannel(ctx, 1)
		s := NewSelector(ctx)
		s.
			AddRecv(c1, func(v interface{}, more bool) {
				assert.True(t, more)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddRecv(c2, func(v interface{}, more bool) {
				assert.True(t, more)
				history = append(history, fmt.Sprintf("c2-%v", v))
			}).
			AddDefault(func() { history = append(history, "default") })
		c1.Send(ctx, "one")
		s.Select(ctx)
		c2.Send(ctx, "two")
		s.Select(ctx)
		s.Select(ctx)
	})
	d.ExecuteUntilAllBlocked()
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
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "add-one")
			c1.Send(ctx, "one")
		})
		Go(ctx, func(ctx Context) {
			history = append(history, "add-two")
			c2.Send(ctx, "two")
		})

		s := NewSelector(ctx)
		s.
			AddRecv(c1, func(v interface{}, more bool) {
				assert.True(t, more)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).
			AddRecv(c2, func(v interface{}, more bool) {
				assert.True(t, more)
				history = append(history, fmt.Sprintf("c2-%v", v))
			})
		history = append(history, "select1")
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	d.ExecuteUntilAllBlocked()
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

func TestSendSelect(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewChannel(ctx)
		c2 := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "receiver")
			v, more := c2.Recv(ctx)
			assert.True(t, more)
			history = append(history, fmt.Sprintf("c2-%v", v))
			v, more = c1.Recv(ctx)

			assert.True(t, more)
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
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())

	expected := []string{
		"select1",
		"receiver",
		"send2",
		"select2",
		"c2-two",
		"send1",
		"done",
		"c1-one",
	}
	require.EqualValues(t, expected, history)
}

func TestChannelClose(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		jobs := NewBufferedChannel(ctx, 5)
		done := NewNamedChannel(ctx, "done")

		GoNamed(ctx, "receiver", func(ctx Context) {
			for {
				j, more := jobs.Recv(ctx)
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
		_, _ = done.Recv(ctx)
		history = append(history, "done")

	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
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
	d := newDispatcher(background, func(ctx Context) {
		defer func() {
			assert.NotNil(t, recover(), "panic expected")
		}()
		c := NewChannel(ctx)
		Go(ctx, func(ctx Context) {
			c.Close()
		})
		c.Send(ctx, "baz")
	})
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())
}

func TestBlockedSendClosedChannel(t *testing.T) {
	d := newDispatcher(background, func(ctx Context) {
		defer func() {
			assert.NotNil(t, recover(), "panic expected")
		}()
		c := NewBufferedChannel(ctx, 5)
		c.Send(ctx, "bar")
		c.Close()
		c.Send(ctx, "baz")
	})
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())
}

func TestAsyncSendClosedChannel(t *testing.T) {
	d := newDispatcher(background, func(ctx Context) {
		defer func() {
			assert.NotNil(t, recover(), "panic expected")
		}()
		c := NewBufferedChannel(ctx, 5)
		c.Send(ctx, "bar")
		c.Close()
		_ = c.SendAsync("baz")
	})
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())
}

func TestDispatchClose(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c := NewNamedChannel(ctx, "forever_blocked")
		for i := 0; i < 10; i++ {
			ii := i
			GoNamed(ctx, fmt.Sprintf("c-%v", i), func(ctx Context) {
				_, _ = c.Recv(ctx) // blocked forever
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
		_, _ = c.Recv(ctx) // blocked forever
	})
	require.EqualValues(t, 0, len(history))
	d.ExecuteUntilAllBlocked()
	require.False(t, d.IsDone())
	stack := d.StackTrace()
	// 11 coroutines (3 lines each) + 10 nl
	require.EqualValues(t, 11*3+10, len(strings.Split(stack, "\n")), stack)
	require.Contains(t, stack, "coroutine 1 [blocked on forever_blocked.Recv]:")
	for i := 0; i < 10; i++ {
		require.Contains(t, stack, fmt.Sprintf("coroutine c-%v [blocked on forever_blocked.Recv]:", i))
	}
	beforeClose := runtime.NumGoroutine()
	d.Close()
	time.Sleep(100 * time.Millisecond) // Let all goroutines to die
	closedCount := beforeClose - runtime.NumGoroutine()
	require.EqualValues(t, 11, closedCount)
	expected := []string{
		"root",
	}
	require.EqualValues(t, expected, history)
}

func TestPanic(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c := NewNamedChannel(ctx, "forever_blocked")
		for i := 0; i < 10; i++ {
			ii := i
			GoNamed(ctx, fmt.Sprintf("c-%v", i), func(ctx Context) {
				if ii == 9 {
					panic("simulated failure")
				}
				_, _ = c.Recv(ctx) // blocked forever
				history = append(history, fmt.Sprintf("child-%v", ii))
			})
		}
		history = append(history, "root")
		_, _ = c.Recv(ctx) // blocked forever
	})
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked()
	require.NotNil(t, err)
	require.EqualValues(t, "simulated failure", err.Value())
	require.EqualValues(t, "simulated failure", err.Error())

	require.Contains(t, err.StackTrace(), "client/cadence.TestPanic")
}

func TestFutureSetValue(t *testing.T) {
	var history []string
	var f Future
	var s Settable
	d := newDispatcher(background, func(ctx Context) {
		f, s = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			assert.False(t, f.IsReady())
			v, err := f.Get(ctx)
			assert.Nil(t, err)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v", v))
			// test second get of the ready future
			v, err = f.Get(ctx)
			assert.Nil(t, err)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v", v))
		})
		history = append(history, "root-end")

	})
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	assert.False(t, f.IsReady())
	s.SetValue("value1")
	assert.True(t, f.IsReady())
	err = d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
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
	d := newDispatcher(background, func(ctx Context) {
		f, s = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			assert.False(t, f.IsReady())
			v, err := f.Get(ctx)
			assert.Nil(t, v)
			assert.NotNil(t, err)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v", err))
			// test second get of the ready future
			v, err = f.Get(ctx)
			assert.Nil(t, v)
			assert.NotNil(t, err)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v", err))
		})
		history = append(history, "root-end")

	})
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	assert.False(t, f.IsReady())
	s.SetError(errors.New("value1"))
	assert.True(t, f.IsReady())
	err = d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
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
	var f Future
	var s Settable
	d := newDispatcher(background, func(ctx Context) {
		f, s = NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			assert.False(t, f.IsReady())
			v, err := f.Get(ctx)
			assert.NotNil(t, err)
			assert.NotNil(t, v)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v-%v", v, err))
			// test second get of the ready future
			v, err = f.Get(ctx)
			assert.NotNil(t, err)
			assert.NotNil(t, v)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v-%v", v, err))
		})
		history = append(history, "root-end")

	})
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	assert.False(t, f.IsReady())
	s.Set("value1", errors.New("error1"))
	assert.True(t, f.IsReady())
	err = d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"future-set",
		"future-get-value1-error1",
		"child-end-value1-error1",
	}
	require.EqualValues(t, expected, history)
}

func TestFutureChain(t *testing.T) {
	var history []string
	var f, cf Future
	var s, cs Settable

	d := newDispatcher(background, func(ctx Context) {
		f, s = NewFuture(ctx)
		cf, cs = NewFuture(ctx)
		s.Chain(cf)
		Go(ctx, func(ctx Context) {
			history = append(history, "child-start")
			assert.False(t, f.IsReady())
			v, err := f.Get(ctx)
			assert.NotNil(t, err)
			assert.NotNil(t, v)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("future-get-%v-%v", v, err))
			// test second get of the ready future
			v, err = f.Get(ctx)
			assert.NotNil(t, err)
			assert.NotNil(t, v)
			assert.True(t, f.IsReady())
			history = append(history, fmt.Sprintf("child-end-%v-%v", v, err))
		})
		history = append(history, "root-end")

	})
	require.EqualValues(t, 0, len(history))
	err := d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.False(t, d.IsDone(), fmt.Sprintf("%v", d.StackTrace()))
	history = append(history, "future-set")
	assert.False(t, f.IsReady())
	cs.Set("value1", errors.New("error1"))
	assert.True(t, f.IsReady())
	err = d.ExecuteUntilAllBlocked()
	if err != nil {
		require.NoError(t, err, err.StackTrace())
	}
	require.True(t, d.IsDone())

	expected := []string{
		"root-end",
		"child-start",
		"future-set",
		"future-get-value1-error1",
		"child-end-value1-error1",
	}
	require.EqualValues(t, expected, history)
}

func TestSelectFuture(t *testing.T) {
	var history []string
	d := newDispatcher(background, func(ctx Context) {
		c1 := NewChannel(ctx)
		future, settable := NewFuture(ctx)
		Go(ctx, func(ctx Context) {
			history = append(history, "add-one")
			c1.Send(ctx, "one")
		})
		Go(ctx, func(ctx Context) {
			history = append(history, "add-two")
			settable.SetValue("two")
		})

		s := NewSelector(ctx)
		s.
			AddRecv(c1, func(v interface{}, more bool) {
				assert.True(t, more)
				history = append(history, fmt.Sprintf("c1-%v", v))
			}).AddFuture(future, func(v interface{}, err error) {
			assert.Nil(t, err)
			history = append(history, fmt.Sprintf("c2-%v", v))
		})
		history = append(history, "select1")
		s.Select(ctx)
		history = append(history, "select2")
		s.Select(ctx)
		history = append(history, "done")
	})
	d.ExecuteUntilAllBlocked()
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
