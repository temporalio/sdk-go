package otlpworker

// InstrumentationScopeName is the OpenTelemetry instrumentation scope used for
// the meter and tracer supplied to the Temporal SDK.
const InstrumentationScopeName = "temporal-sdk"

// FirstNonEmptyEnv resolves a configuration value.
//
// It returns explicit when it is non-empty. Otherwise it returns the value of
// the first environment variable in names (looked up via getenv) whose value is
// non-empty. If none is set, it returns fallback.
//
// An environment value is treated as unset only when it is the empty string;
// values are not trimmed. This matches the pre-existing AWS Lambda resolution
// behavior so that refactoring onto this shared helper changes nothing for
// existing callers.
//
// getenv is normally os.Getenv; it is a parameter so callers can test resolution
// without mutating the process environment.
func FirstNonEmptyEnv(explicit string, getenv func(string) string, names []string, fallback string) string {
	if explicit != "" {
		return explicit
	}
	for _, name := range names {
		if value := getenv(name); value != "" {
			return value
		}
	}
	return fallback
}
