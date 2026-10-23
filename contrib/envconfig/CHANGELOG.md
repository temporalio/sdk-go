<!--
Release notes for go.temporal.io/sdk/contrib/envconfig.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Experimental client config extensions. Extensions implement `ClientConfigExtension`, read their own config from
  `profile.<profile>.ext.<name>` TOML tables (exposed as `ClientConfigProfile.Ext`) and env vars, and are passed via
  `LoadClientOptionsRequest.Extensions` to contribute client plugins to the loaded options. Strict mode does not
  reject keys under `ext`, and `ext` is preserved by `ClientConfig.ToTOML`.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
