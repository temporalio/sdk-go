package log

// NewNopLogger creates a Logger implementation that discards all log messages.
// This is useful for testing or when logging is not desired.
func NewNopLogger() Logger {
	return nopLogger{}
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...interface{}) {}
func (nopLogger) Info(string, ...interface{})  {}
func (nopLogger) Warn(string, ...interface{})  {}
func (nopLogger) Error(string, ...interface{}) {}
func (nopLogger) With(...interface{}) Logger   { return nopLogger{} }
func (nopLogger) WithCallerSkip(int) Logger    { return nopLogger{} }
