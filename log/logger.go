package log

type (
	// Logger is an interface that can be passed to ClientOptions.Logger.
	Logger interface {
		Debug(msg string, keyvals ...any)
		Info(msg string, keyvals ...any)
		Warn(msg string, keyvals ...any)
		Error(msg string, keyvals ...any)
	}

	// WithSkipCallers is an optional interface that a Logger can implement that
	// may create a new child logger that skips the number of stack frames of the caller.
	// This call must not mutate the original logger.
	WithSkipCallers interface {
		WithCallerSkip(int) Logger
	}

	// WithLogger is an optional interface that prepend every log entry with keyvals.
	// This call must not mutate the original logger.
	WithLogger interface {
		With(keyvals ...any) Logger
	}
)
