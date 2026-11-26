# Changelog

## [0.6.0] - 2026-10-08

### Fixed

- Fixed `workflowcheck` sometimes missing non-deterministic code reached through
  a same-package call cycle, such as inside `encoding/json`. A workflow calling
  such code could pass on one run and fail on the next; it is now reported on
  every run. Projects with intermittent `workflowcheck` failures may see them
  become consistent.
- Reasons are now reported in a stable source order instead of varying between
  runs.

