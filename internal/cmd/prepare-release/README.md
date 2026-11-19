This is a script that helps automate releasing the Go SDK and its contrib modules. It validates
changelogs, checks go.mod dependencies, and checks that the new version number follows after
the latest release. After validating, the script updates the changelog and any other necessary
release files, creates a draft PR, and creates a draft release.

To see a sample execution, check out [main_test.go](./main_test.go).

# Organization

The package is organized as follows:

- `main.go` the entry point and the main workflow.
- `worktree.go` implements specific operations performed on a temporary worktree, like validation.
- `effects.go` dependency injection object for working with the filesystem and the network.
- Pure helper functions and definitions:
  - `target.go` defines a release target, like the Go SDK or `contrib/envconfig`.
  - `changelog.go` pure functions that validate and update changelog contents.
  - `gomod.go` pure functions that validate and update `go.mod` contents.
  - `version.go` pure functions that validate and update version numbers and `version.go`.
- Testing:
  - `main_test.go` the test harness
  - `*_test.go` tests for individual files.