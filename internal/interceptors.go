package internal

import "time"

type ActivityInterceptor interface {
	ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future
	ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) Future
}

type TimeInterceptor interface {
	Now(ctx Context) time.Time
	NewTimer(ctx Context, d time.Duration) Future
	Sleep(ctx Context, d time.Duration) (err error)
}
