package workflow

import (
	"time"

	"go.temporal.io/sdk/internal"
)

type RegisterOptions struct{}

type Context = internal.Context

func AwaitWithTimeout(ctx Context, timeout time.Duration, condition func() bool) (ok bool, err error) {
	// Intentionally simulate non-deterministic call internally
	time.Sleep(10 * time.Second)
	return false, nil
}

func SideEffect(ctx Context, f func(ctx Context) interface{}) interface{} {
	return nil
}
