<!--
Release notes for go.temporal.io/sdk/contrib/aws/secretsmanager.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

### Changed

### Deprecated

### :boom: Breaking Changes

### Fixed

### Security

## [0.1.0] - 2026-11-10

### Added

- Added envconfig `ClientConfigExtension` for reading client credentials
  (API key or TLS) from AWS Secrets Manager, based on configured secret IDs
  within the `profile.<profile>.ext.aws_secrets_manager` TOML table, or by
  `TEMPORAL_EXT_AWS_SECRETS_MANAGER_*` environment variables.

