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

- AdditionalProfileFields allows for extra fields at any level of nesting
within a profile, rather than just the top level

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
- Config files with duplicate array keys now return an error due to a bug fix in BurntSushi/toml v1.6.0.
