<!--
Release notes for go.temporal.io/sdk/contrib/workflowstreams.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Fixed

- Subscriptions now query and deliver the remaining retained items after workflow completion, failure, cancellation, termination, or timeout. Upgrade workers before clients to register the new poll query handler.

### Breaking Changes

- Raised the minimum supported Go version from 1.25.4 to 1.26.0.
