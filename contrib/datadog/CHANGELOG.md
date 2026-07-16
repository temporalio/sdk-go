<!--
Release notes for go.temporal.io/sdk/contrib/datadog.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Security

- Upgrade `github.com/DataDog/dd-trace-go/v2` to v2.8.1 to address a potential
  denial of service when extracting W3C baggage headers.
