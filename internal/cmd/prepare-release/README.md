This is a script that helps automate releasing the Go SDK and its contrib modules. It validates
changelogs, checks go.mod dependencies, and checks that the new version number follows after
the latest release. After validating, the script updates the changelog and any other necessary
release files, creates a draft PR, and creates a draft release.

To see a sample execution, check out [main_test.go](./main_test.go).

For main SDK releases, the draft PR also lists contrib modules with changes since
each module's latest stable release tag. Commits that only change `go.mod` or
`go.sum`, including those in nested test modules, are excluded.
The list includes docs and other file changes, so the release owner decides which
modules need a separate release.

To check contrib changes independently of an SDK release, run from the repository root:

```sh
go run ./internal/cmd/prepare-release contrib-check
```

This fetches `main` and tags from `origin`, then prints the same summary against
`origin/main`. It does not change your working files or create a release worktree,
PR, or draft release. Uncommitted changes and local branch commits are not included.

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
- `contrib.go` discovers contrib release tags and builds the PR's contrib commit summary.
- Testing:
  - `main_test.go` the test harness
  - `*_test.go` tests for individual files.
