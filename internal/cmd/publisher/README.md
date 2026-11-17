# Organization

The package is organized as follows:

- `main.go` contains the entry point and the `gate`, `find`, `stage`, `vet`, and `tag` commands.
- `main_test.go` shows you how each command affects the filesystem and what commands it runs.
- `repo.go` owns operations on the original git checkout.
- `worktree.go` owns operations on the git worktree created by most commands.
- `effects.go` dependency injection object for I/O, networking, RNG, and more.
- `coretool.go` wraps the `changelog-tool` binary from sdk-rust.
- `pure/` contains helpers that validate release files and don't perform risky I/O:
  - `pure/module.go` defines a Go module like the Go SDK itself or a `contrib/` module.
  - `pure/gomod.go` defines a `go.mod` file.
  - `pure/version.go` defines operations on version numbers.
