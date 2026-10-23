<!--
Release notes for go.temporal.io/sdk/contrib/aws/lambdaworker.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Experimental `Options.IdleTimeout` (or the `TEMPORAL_LAMBDA_IDLE_TIMEOUT` environment variable)
  to gracefully shut the worker down before the invocation deadline once it has had no task in
  hand for at least the given duration, plus random jitter. Disabled by default.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.

### Fixed

- Setting `WorkerOptions.Tuner` in the configure callback no longer causes a panic from the
  conflicting Lambda concurrency defaults. When a tuner is set, the `MaxConcurrent*ExecutionSize`
  options are ignored.
