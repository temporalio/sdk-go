<!--
Release notes for go.temporal.io/sdk/contrib/aws/lambdaworker/otel.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Changed

- Internal refactor to build providers, resolve configuration, install the
  metrics handler and tracing interceptor, and flush through the shared
  `go.temporal.io/sdk/contrib/opentelemetry/otlpworker` module. No public API
  change.
