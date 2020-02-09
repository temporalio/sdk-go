package internal

type ActivityInterceptor interface {
	ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future
	ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) Future
}
