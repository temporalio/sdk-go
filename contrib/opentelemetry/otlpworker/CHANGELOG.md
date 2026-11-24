# Changelog

## [0.1.1] - 2026-11-24

### Fixed

- Fixed `NewProviders` failing with a resource schema conflict when used with OpenTelemetry SDK v1.45.0. The resource containing `service.name` no longer specifies a schema URL, allowing it to inherit the default resource's schema.

### Security

- Updated OpenTelemetry to v1.47.0 to fix an [attribute length limit bypass](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-q7gw-24fr-5p69) and [excessive memory allocation when parsing baggage](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-rrfc-gjm9-cgrj).

## [0.1.0] - 2026-09-29

### Added

- Initial release of the provider-neutral OTLP worker OpenTelemetry glue shared
  by the AWS Lambda and Google Cloud Run worker integrations: `NewProviders`,
  `Apply`/`ApplyMetrics`/`ApplyTracing`, `FirstNonEmptyEnv`, `ForceFlush`, and
  `Shutdown`.

