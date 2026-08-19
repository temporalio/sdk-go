<!--
Release notes for go.temporal.io/sdk/contrib/opentelemetry/otlpworker.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Initial release of the provider-neutral OTLP worker OpenTelemetry glue shared
  by the AWS Lambda and Google Cloud Run worker integrations: `NewProviders`,
  `Apply`/`ApplyMetrics`/`ApplyTracing`, `FirstNonEmptyEnv`, `ForceFlush`, and
  `Shutdown`.
