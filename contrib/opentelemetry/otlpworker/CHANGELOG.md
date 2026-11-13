## [Unreleased]

### Added

### Changed

### Deprecated

### :boom: Breaking Changes

### Fixed

- Fixed `NewProviders` failing with a resource schema conflict when used with
  OpenTelemetry SDK v1.45.0. The resource containing `service.name` no longer
  specifies a schema URL, allowing it to inherit the default resource's schema.

### Security

## [0.1.0] - 2026-09-29

### Added

- Initial release of the provider-neutral OTLP worker OpenTelemetry glue shared
  by the AWS Lambda and Google Cloud Run worker integrations: `NewProviders`,
  `Apply`/`ApplyMetrics`/`ApplyTracing`, `FirstNonEmptyEnv`, `ForceFlush`, and
  `Shutdown`.

