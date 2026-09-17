package otlpworker

import (
	"context"
	"errors"
)

// ForceFlush concurrently force-flushes every provider that implements
// ForceFlush(context.Context) error, joining any errors. Providers that do not
// implement ForceFlush are ignored. This exports buffered telemetry without
// shutting the providers down.
func ForceFlush(ctx context.Context, providers ...any) error {
	functions := make([]func(context.Context) error, 0, len(providers))
	for _, provider := range providers {
		if flusher, ok := provider.(interface {
			ForceFlush(context.Context) error
		}); ok {
			functions = append(functions, flusher.ForceFlush)
		}
	}
	return runConcurrently(ctx, functions...)
}

// Shutdown concurrently shuts down every provider that implements
// Shutdown(context.Context) error, joining any errors. Providers that do not
// implement Shutdown are ignored.
func Shutdown(ctx context.Context, providers ...any) error {
	functions := make([]func(context.Context) error, 0, len(providers))
	for _, provider := range providers {
		if shutdowner, ok := provider.(interface {
			Shutdown(context.Context) error
		}); ok {
			functions = append(functions, shutdowner.Shutdown)
		}
	}
	return runConcurrently(ctx, functions...)
}

func runConcurrently(ctx context.Context, functions ...func(context.Context) error) error {
	if len(functions) == 0 {
		return nil
	}
	results := make(chan error, len(functions))
	for _, function := range functions {
		go func(fn func(context.Context) error) { results <- fn(ctx) }(function)
	}
	errs := make([]error, 0, len(functions))
	for range functions {
		errs = append(errs, <-results)
	}
	return errors.Join(errs...)
}
