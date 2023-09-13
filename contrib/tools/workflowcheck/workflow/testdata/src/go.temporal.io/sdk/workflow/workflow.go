package workflow

import "time"

type RegisterOptions struct{}

type Context interface{}

func AwaitWithTimeout(ctx Context, timeout time.Duration, condition func() bool) (ok bool, err error) {
	// Intentionally simulate non-deterministic call internally
	time.Sleep(10 * time.Second)
	return false, nil
}

func SideEffect(ctx Context, f func(ctx Context) interface{}) interface{} {
	return nil
}
