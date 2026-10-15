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

## Extensions

Separately packaged modules can add their own configuration and client behavior
by implementing `envconfig.ClientConfigExtension`. Each extension owns a TOML
table under `ext` in each profile:

```toml
[profile.default.ext.my_extension]
some_field = "value"
```

Pass extensions to `LoadClientOptions`; each is called in order with the loaded
profile and environment, and any client plugin it returns is appended to
`client.Options.Plugins`:

```go
options, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
	Extensions: []envconfig.ClientConfigExtension{myextension.Extension{}},
})
```

Extensions are recommended to use a `TEMPORAL_EXT_<NAME>_` prefix for their
environment variables. See `ExampleClientConfigExtension` in `example_extension_test.go` for a complete extension.
