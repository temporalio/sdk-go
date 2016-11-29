package coroutine

import (
	//"fmt"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDispatcher(t *testing.T) {
	value := "foo"
	d := NewDispatcher(func(ctx Context) { value = "bar" })
	require.Equal(t, "foo", value)
	d.ExecuteUntilAllBlocked()
	require.True(t, d.IsDone())
	require.Equal(t, "bar", value)
}

func TestNonBlockingChildren(t *testing.T) {
	var history []string
	d := NewDispatcher(func(ctx Context) {
		for i := 0; i < 10; i++ {
			ii := i
			ctx.NewCoroutine(func(ctx Context) {
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewChannel()
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "child-start")
			v := c1.Recv(ctx)
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewBufferedChannel(1)
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "child-start")
			v1 := c1.Recv(ctx)
			history = append(history, fmt.Sprintf("child-end-%v", v1))
			v2 := c1.Recv(ctx)
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewChannel()
		c2 := ctx.NewBufferedChannel(2)

		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "child1-start")
			c2.Send(ctx, "bar1")
			history = append(history, "child1-get")
			v1 := c1.Recv(ctx)
			history = append(history, fmt.Sprintf("child1-end-%v", v1))

		})
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "child2-start")
			c2.Send(ctx, "bar2")
			history = append(history, "child2-get")
			v1 := c1.Recv(ctx)
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewBufferedChannel(1)
		c2 := ctx.NewBufferedChannel(1)
		s := ctx.NewSelector()
		s.
			AddRecv(c1, func(v interface{}) { history = append(history, fmt.Sprintf("c1-%v", v)) }).
			AddRecv(c2, func(v interface{}) { history = append(history, fmt.Sprintf("c2-%v", v)) }).
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewChannel()
		c2 := ctx.NewChannel()
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "add-one")
			c1.Send(ctx, "one")
		})
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "add-two")
			c2.Send(ctx, "two")
		})

		s := ctx.NewSelector()
		s.
			AddRecv(c1, func(v interface{}) { history = append(history, fmt.Sprintf("c1-%v", v)) }).
			AddRecv(c2, func(v interface{}) { history = append(history, fmt.Sprintf("c2-%v", v)) })
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
	d := NewDispatcher(func(ctx Context) {
		c1 := ctx.NewChannel()
		c2 := ctx.NewChannel()
		ctx.NewCoroutine(func(ctx Context) {
			history = append(history, "receiver")
			v := c2.Recv(ctx)
			history = append(history, fmt.Sprintf("c2-%v", v))
			v = c1.Recv(ctx)
			history = append(history, fmt.Sprintf("c1-%v", v))

		})
		s := ctx.NewSelector()
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
