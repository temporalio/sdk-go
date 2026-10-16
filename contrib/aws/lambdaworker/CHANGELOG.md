# Changelog

## [Unreleased]

### Added

- Added experimental idle shutdown. Set `Options.IdleTimeout`, or the
  `TEMPORAL_SERVERLESS_WORKER_IDLE_TIMEOUT` environment variable, to shut down an invocation early
  once the worker has been idle for that long. Disabled by default.

### Changed

### Deprecated

### :boom: Breaking Changes

### Fixed

### Security

## [0.2.0] - 2026-11-12

### Added

- Client credentials can be loaded from AWS Secrets Manager. `RunWorker` now
  loads client options with the `go.temporal.io/sdk/contrib/aws/secretsmanager`
  envconfig extension, so the API key and TLS cert/key/CA can be configured as
  secret IDs via `TEMPORAL_EXT_AWS_SECRETS_MANAGER_*` env vars or
  `profile.<profile>.ext.aws_secrets_manager` in the config file.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.

