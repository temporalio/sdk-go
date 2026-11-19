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

type SideEffectOptions struct{}

type MutableSideEffectOptions struct{}

func SideEffect(ctx Context, f func(ctx Context) interface{}) interface{} {
	return nil
}

func SideEffectWithOptions(ctx Context, options SideEffectOptions, f func(ctx Context) interface{}) interface{} {
	return nil
}

func MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) interface{} {
	return nil
}

func MutableSideEffectWithOptions(ctx Context, id string, options MutableSideEffectOptions, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) interface{} {
	return nil
}
