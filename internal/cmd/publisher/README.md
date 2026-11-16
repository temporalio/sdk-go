This is a script that helps automate releasing the Go SDK and its contrib modules.
Prepare a draft PR, run lint-release on its commit, then create draft releases after merging.

## Changelog tool setup

Publisher invokes `cargo run --quiet` from a local sdk-rust checkout.
Install Rust and Cargo, and check out sdk-rust beside sdk-go at the revision in
`sdk-rust-revision`. Set `PUBLISHER_SDK_RUST` to use a checkout elsewhere.
The lint, PR, lint-release, and release commands verify its HEAD once during
setup, before creating a temporary worktree or validating modules. Discovery does
not need the tool. Publisher does not fetch sdk-rust or change its Git revision.
Uncommitted tool edits are used, which allows testing coordinated changes.
Cargo may generate the workspace's ignored `Cargo.lock` and build artifacts.
The source revision is pinned, but dependency resolution is not.

Each module keeps pending notes in `changelog/<category>/<name>.md` relative to its
module directory. Categories are `added`, `stabilized`, `changed`, `deprecated`,
`breaking-changes`, `fixed`, and `security`. Each nonempty line becomes one bullet;
write entries without leading list markers. There is no Unreleased section.

`publisher find` scans SDK and contrib modules on `origin/main` after fetching
origin and release tags. It checks for commits since each module's highest stable
release tag and asks you to choose minor `[m]`,
patch `[p]`, view commit log `[c]`, view diff `[d]`, or skip `[s]`. Empty input also skips.
It prints a `publisher pr` command for the selected versions without running it.
If no modules are selected, it prints no command or summary.

The module list is computed once from the checkout's `HEAD`. A module is eligible
for release discovery if it has a `CHANGELOG.md` file or a `changelog/` directory.
CI uses the same eligibility rule and validates fragments even when there is no
`CHANGELOG.md` yet. Commit discovery excludes dependency-only changes (`go.mod`
and `go.sum`) and changes belonging to nested releasable modules. Changes in
test-only nested modules count toward their containing releasable module.
Local edits and commits outside `origin/main` are not included.
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

`publisher lint [-skip-changelog] BASE_COMMIT` validates fragments and existing
changelogs in the checkout for the SDK and releasable contrib modules. Each module
with changes between `BASE_COMMIT` and `HEAD` must have a new fragment of its own.
Change detection uses the same ownership rules as release discovery: dependency-only
changes are ignored, nested releasable modules are checked separately, and test-only
nested modules belong to their parent. A fragment for one module does not satisfy
another module's requirement.
Use `-skip-changelog` to waive only the new-fragment requirement; validation still
runs for every module. CI sets this flag from the `skip-changelog` PR label.
The base commit must already be available locally; this command does not fetch.
Validation reads files from the working tree, but module discovery and the
new-fragment check use committed files.

`publisher pr MODULE=VERSION [MODULE=VERSION ...]` updates release files and creates a
draft PR using the specified versions. For example,
`publisher pr sdk=1.48.0 contrib/envconfig=1.0.3` prepares SDK version `1.48.0` and
envconfig version `1.0.3`. Versions must use `major.minor.patch` format, without
prerelease or build metadata.

PR preparation validates fragments for all selected modules, then calls the shared
tool to assemble dated changelog sections and delete consumed fragments. Empty
releases are allowed; duplicate versions and leftover Unreleased sections are
rejected. Only selected modules are prepared. Version-file changes are still owned
by publisher. A preparation failure can partially modify the temporary worktree,
but publisher does not commit or push it and removes it during cleanup.

`publisher release COMMIT MODULE=VERSION [MODULE=VERSION ...]` still takes explicit
versions and validates the merged release commit before creating draft releases.

To see a sample execution, check out [main_test.go](./main_test.go).

`publisher lint-release COMMIT MODULE=VERSION [MODULE=VERSION ...]` validates the specified
releases in a temporary snapshot after fetching origin. For example,
`publisher lint-release my-release sdk=1.48.0 contrib/envconfig=1.0.3` checks those two releases.
Specify `HEAD`, a branch, a tag, or a commit SHA. There is no default commit.
Uncommitted local edits are not included. Inspection checks module metadata,
published Temporal dependencies, the SDK version constant, and changelog sections
against the supplied release specs. The selected changelog sections must be dated
today, using the machine's local date in `YYYY-MM-DD` format. Both lint-release and release
enforce this check. It does not infer versions or compare them
with existing release tags.

Lint-release uses the shared tool's `release-notes --date` command, discarding its output.
It does not require pending
fragment directories to be empty: notes merged after preparation belong to the next
release. Publishing uses `release-notes` from the completed version section, with
the `## Notable Changes` wrapper, and never includes pending fragments.

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
  `find`, `lint`, `pr`, `lint-release`, and `release` commands, each with its own
  constructor, options, and workflow.
  Commands that use the shared tool initialize it locally. This file also coordinates
  module validation for lint-release and release.
- `repo.go` owns operations on the original checkout, including fetching, resolving
  commits, discovering contrib changes, and creating and removing temporary worktrees.
- `worktree.go` implements operations performed on a checkout or temporary worktree, like
  release-file preparation and draft PR and release creation.
- `effects.go` dependency injection object for console input/output,
  the clock, the filesystem, and the network.
  It also contains command formatting and module-proxy path escaping helpers.
- `coretool.go` locates and verifies the pinned sdk-rust checkout, then invokes the
  shared tool for fragment validation, changelog preparation, completed-release
  validation, and publishing notes. Each wrapper constructs its Rust CLI arguments;
  the shared runner invokes Cargo.
- `pure/` contains helpers that take explicit data inputs, with no dependency on `app`,
  `worktree`, the filesystem, or the network:
  - `pure/module.go` defines a `Module`, like the Go SDK or `contrib/envconfig`, and a
    `VersionedModule` pairing a module with the version to release.
  - `pure/gomod.go` validates module declarations and Temporal requirements in `go.mod`.
  - `pure/version.go` validates and updates version numbers and `version.go` contents.
