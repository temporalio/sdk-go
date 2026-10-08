<!--
Release notes for go.temporal.io/sdk/contrib/tools/workflowcheck.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Changed

- Recommend running `workflowcheck` as a module tool so it uses the Go toolchain selected by the module being analyzed.

### Fixed

- Fixed `workflowcheck` sometimes missing non-deterministic code reached through
  a same-package call cycle, such as inside `encoding/json`. A workflow calling
  such code could pass on one run and fail on the next; it is now reported on
  every run. Projects with intermittent `workflowcheck` failures may see them
  become consistent.
- Reasons are now reported in a stable source order instead of varying between
  runs.
