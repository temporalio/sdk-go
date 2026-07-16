# Environment configuration for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/envconfig` loads Temporal client options
from TOML configuration files and environment variables. Environment variables
override values loaded from a file.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/envconfig@latest
```

## Module versioning

`envconfig` is released as a separate Go module from the core Temporal Go SDK.
See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Load the default profile and pass the resulting options to the Temporal client:

```go
options, err := envconfig.LoadDefaultClientOptions()
if err != nil {
	return err
}

c, err := client.Dial(options)
```

By default, configuration is loaded from `TEMPORAL_CONFIG_FILE` or the
platform-specific user configuration directory, then overridden by supported
`TEMPORAL_` environment variables. See the [package documentation](https://pkg.go.dev/go.temporal.io/sdk/contrib/envconfig)
for profiles, file formats, and loading options.
