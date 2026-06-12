package internal

type Context interface{}

func ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) interface{} {
	return nil
}
