package log

import (
	"go.temporal.io/sdk/log"
)

// NoopLogger is Logger implementation that doesn't produce any logs.
type NoopLogger struct {
}

// NewNopLogger creates new instance of NoopLogger.
func NewNopLogger() *NoopLogger {
	return &NoopLogger{}
}

// Debug does nothing.
func (l *NoopLogger) Debug(string, ...any) {}

// Info does nothing.
func (l *NoopLogger) Info(string, ...any) {}

// Warn does nothing.
func (l *NoopLogger) Warn(string, ...any) {}

// Error does nothing.
func (l *NoopLogger) Error(string, ...any) {}

// With returns new NoopLogger.
func (l *NoopLogger) With(...any) log.Logger {
	return NewNopLogger()
}
