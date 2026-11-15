This is a script that helps automate releasing the Go SDK and its contrib modules.
Prepare a draft PR, lint its commit, then create draft releases after merging.

`publisher find` scans SDK and contrib modules on `origin/main` after fetching
origin and release tags. It checks for commits since each module's highest stable
release tag and asks you to choose minor `[m]`,
patch `[p]`, view commit log `[c]`, view diff `[d]`, or skip `[s]`. Empty input also skips.
It prints a `publisher pr` command for the selected versions without running it.
If no modules are selected, it prints no command or summary.

The module list is computed once from the checkout's `HEAD`, including contrib
directories with `go.mod` even without a changelog. Commit discovery excludes
dependency-only changes (`go.mod` and `go.sum`) and changes belonging to nested
modules. Local edits and commits outside `origin/main` are not included.
A never-released module offers `0.1.0` for minor or `0.0.1` for patch.
The scan is advisory: commits that were later reverted still appear.

Press a choice key without Enter. Single-key input requires `bash` on PATH.
Enter alone skips the module. `[c]` shows commit dates, abbreviated hashes,
subjects, and GitHub commit URLs in `less`, or prints them if `less` is unavailable.
`[d]` runs `git --paginate diff`, using your Git pager and color settings. Exit the
pager to return to the release prompt. Both commands use the module's matching
files and the selected commit; diffs compare against the last release, or an empty
tree for never-released modules.
EOF cancels without printing a release command, even if earlier modules were selected.

`publisher pr MODULE=VERSION [MODULE=VERSION ...]` updates release files and creates a
draft PR using the specified versions. For example,
`publisher pr sdk=1.48.0 contrib/envconfig=1.0.3` prepares SDK version `1.48.0` and
envconfig version `1.0.3`. Versions must use `major.minor.patch` format, without
prerelease or build metadata.

`publisher release COMMIT MODULE=VERSION [MODULE=VERSION ...]` still takes explicit
versions and validates the merged release commit before creating draft releases.

To see a sample execution, check out [main_test.go](./main_test.go).

`publisher lint COMMIT MODULE=VERSION [MODULE=VERSION ...]` validates the specified
releases in a temporary snapshot after fetching origin. For example,
`publisher lint my-release sdk=1.48.0 contrib/envconfig=1.0.3` checks those two releases.
Specify `HEAD`, a branch, a tag, or a commit SHA. There is no default commit.
Uncommitted local edits are not included. Inspection checks module metadata,
published Temporal dependencies, the SDK version constant, and changelog sections
against the supplied release specs. The selected changelog sections must be dated
today, using the machine's local date in `YYYY-MM-DD` format. Both lint and release
enforce this check. It does not infer versions or compare them
with existing release tags.

Each invocation prints the path to a private `publisher-*.log` file in the system
temporary directory. The log shows a `# Running in <dir>:` comment above each command,
prefixed with `$ ` and followed by its stdout and stderr, with a blank line between
commands. HTTP lookups are also logged.
Interactive Git viewer commands are logged, but their terminal output is not captured.
Progress, results, and suggested
next-step and rollback commands remain on screen. Logs survive worktree cleanup,
but the operating system may eventually remove temporary files; copy them elsewhere
if you need to retain them. Command arguments are logged, so do not pass credentials
as arguments.

# Organization

The package is organized as follows:

- `main.go` contains the entry point, shared `app` dependencies, and the
  `find`, `lint`, `pr`, and `release` commands, each with its own constructor, options, and workflow.
  It also coordinates module validation for lint and release.
- `repo.go` owns operations on the original checkout, including fetching, resolving
  commits, discovering contrib changes, and creating and removing temporary worktrees.
- `worktree.go` implements operations performed on a checkout or temporary worktree, like
  release-file preparation and draft PR and release creation.
- `effects.go` dependency injection object for console input/output,
  the clock, the filesystem, and the network.
  It also contains command formatting and module-proxy path escaping helpers.
- `pure/` contains helpers that take explicit data inputs, with no dependency on `app`,
  `worktree`, the filesystem, or the network:
  - `pure/changelog.go` validates and updates changelog contents.
  - `pure/module.go` defines a `Module`, like the Go SDK or `contrib/envconfig`, and a
    `VersionedModule` pairing a module with the version to release.
  - `pure/gomod.go` validates module declarations and Temporal requirements in `go.mod`.
  - `pure/version.go` validates and updates version numbers and `version.go` contents.
