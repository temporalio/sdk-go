This is a script that helps automate releasing the Go SDK and its contrib modules. It validates
changelogs, checks go.mod dependencies, and checks that the new version number follows after
the latest release. After validating, the script updates the changelog and any other necessary
release files, creates a draft PR, and creates a draft release.

To see a sample execution, check out [main_test.go](./main_test.go).

# Organization

The package is organized as follows:

- `main.go` the entry point and the lint, PR preparation, and draft release workflows.
- `worktree.go` implements operations performed on a checkout or temporary worktree, like
  validation, release-file preparation, and draft PR and release creation.
- `discovery.go` finds modules changed since their latest stable releases, ignoring
  changes limited to a module's `go.mod` and `go.sum`.
- `effects.go` dependency injection object for working with the filesystem and the network.
- Pure helper functions and definitions:
  - `target.go` defines a release target, like the Go SDK or `contrib/envconfig`, and a release
    specification pairing a target with the version to release.
  - `changelog.go` pure functions that validate and update changelog contents.
  - `gomod.go` pure functions that validate module declarations and Temporal requirements in `go.mod`.
  - `version.go` pure functions that validate and update version numbers and `version.go`.
