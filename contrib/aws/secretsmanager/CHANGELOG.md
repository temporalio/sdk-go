# Changelog

## [0.1.0] - 2026-11-10

### Added

- Added envconfig `ClientConfigExtension` for reading client credentials
  (API key or TLS) from AWS Secrets Manager, based on configured secret IDs
  within the `profile.<profile>.ext.aws_secrets_manager` TOML table, or by
  `TEMPORAL_EXT_AWS_SECRETS_MANAGER_*` environment variables.

