# internal/temporalapi

Temporary home for protobuf definitions that belong in `go.temporal.io/api` but are not yet published there. Once the upstream package ships these types, the generated Go files will be removed and import paths updated to point to `go.temporal.io/api`.

## Structure

Proto source files and their generated Go counterparts live side by side under `sdk/v1/`.

## Regenerating protobuf code

### Prerequisites

**buf** is required to generate Go code from the `.proto` files:

```sh
make install-buf
```

This installs `buf` v1.27.0 via `go install`. If `~/go/bin` is not on your `PATH`, add it:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

### Generate

From this directory (`internal/temporalapi/`):

```sh
make proto
```

Or from the repo root:

```sh
make -C internal/temporalapi proto
```

The generated `.pb.go` files are committed to the repository, so regeneration is only needed when a `.proto` file changes.
