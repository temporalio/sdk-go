# AWS Secrets Manager for Temporal Client Config

> ⚠️ **This package is currently at an experimental release stage.** ⚠️

Package `go.temporal.io/sdk/contrib/aws/secretsmanager` is an
[`envconfig`](../../envconfig/README.md) extension that loads Temporal client
credentials (API key and/or mTLS material) from
[AWS Secrets Manager](https://aws.amazon.com/secrets-manager/). Only secret IDs
live in your config; secret material is fetched at runtime and kept in memory.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/aws/secretsmanager@latest
```

## Module versioning

`secretsmanager` is released as a separate Go module from the core Temporal Go
SDK. See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

```go
import (
    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/contrib/aws/secretsmanager"
    "go.temporal.io/sdk/contrib/envconfig"
)

opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
    Extensions: []envconfig.ClientConfigExtension{secretsmanager.Extension{}},
})
if err != nil {
    // handle error
}
c, err := client.Dial(opts)
```

## Configuration

Each credential is configured with a Secrets Manager secret ID (friendly name
or full ARN), either in the TOML config file or with environment variables.
Environment variables take precedence over the file.

| TOML (under `profile.<profile>.ext.aws_secrets_manager`) | Environment variable | Replaces |
|---|---|---|
| `api_key_id` | `TEMPORAL_EXT_AWS_SECRETS_MANAGER_API_KEY_ID` | `api_key` / `TEMPORAL_API_KEY` |
| `tls.client_cert_id` | `TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_CERT_ID` | `tls.client_cert_{path,data}` / `TEMPORAL_TLS_CLIENT_CERT_{PATH,DATA}` |
| `tls.client_key_id` | `TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_KEY_ID` | `tls.client_key_{path,data}` / `TEMPORAL_TLS_CLIENT_KEY_{PATH,DATA}` |
| `tls.server_ca_cert_id` | `TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_SERVER_CA_CERT_ID` | `tls.server_ca_cert_{path,data}` / `TEMPORAL_TLS_SERVER_CA_CERT_{PATH,DATA}` |

For example:

```toml
[profile.default]
address = "my-namespace.a1b2c.tmprl.cloud:7233"
namespace = "my-namespace.a1b2c"

[profile.default.ext.aws_secrets_manager.tls]
client_cert_id = "temporal/client-cert-and-key"
client_key_id = "temporal/client-cert-and-key"
```

Rules:

- A secret ID can't be combined with the credential it replaces, from any
  source. For example, `api_key` in the file with
  `TEMPORAL_EXT_AWS_SECRETS_MANAGER_API_KEY_ID` in the environment is an error.
- The client cert and key IDs must be set together. They may be the same ID,
  in which case the secret holds both the PEM certificate and the PEM key. This
  lets the pair be rotated atomically.
- TLS secret IDs can't be used when TLS is disabled.
- An API key secret enables TLS by default, as `api_key` does.
- Secret values are used as-is: the API key as a string, and TLS material as
  PEM. Both `SecretString` and `SecretBinary` secrets are supported.
- Unknown keys under `ext.aws_secrets_manager` are rejected.

## Fetching and caching

When `Extension.SecretsClient` is nil, secrets are fetched through the
[AWS Parameters and Secrets Lambda Extension](https://docs.aws.amazon.com/secretsmanager/latest/userguide/retrieving-secrets_lambda.html)
if it is present. Otherwise they are fetched directly from Secrets Manager with
an AWS SDK v2 client built from the default AWS config.

The Lambda extension is detected as follows:

- If `PARAMETERS_SECRETS_EXTENSION_HTTP_PORT` is set to a port, the extension
  is used on that port.
- If it is unset, the extension's default port, 2773, is probed, and the
  extension is used if something is listening there.
- Setting `PARAMETERS_SECRETS_EXTENSION_HTTP_PORT` to an empty value explicitly
  disables detection so that secrets are always fetched directly. Use this if
  another service in your function listens on port 2773.

To use your own client, for example
with a specific region or credentials, set `Extension.SecretsClient`:

```go
cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
// ...
ext := secretsmanager.Extension{SecretsClient: awssm.NewFromConfig(cfg)}
```

Secrets are first fetched when a client is created from the loaded options,
and client creation fails if a fetch fails. After that:

- Secrets are cached by the plugin that the extension adds to the loaded
  options, so clients created from the same options share the cache (for
  example, across warm Lambda invocations).
- The API key and client cert/key are refreshed while a client is running once
  the refresh interval has elapsed, so rotated secrets are picked up without
  recreating the client.
- The refresh interval is `Extension.RefreshInterval` if set. Otherwise it
  comes from the `SECRETS_MANAGER_TTL` environment variable, interpreted the
  same way as by the Lambda extension: whole seconds, where 0 disables
  caching. Unlike the Lambda extension, values over 300 are allowed. If neither is set, it defaults to 5 minutes. An invalid
  `SECRETS_MANAGER_TTL` is an error when loading client options.
- If a refresh fails, the last fetched value keeps being used for up to
  `Extension.MaxStaleness` (default 24 hours) after it was fetched. After a
  failed refresh, the next fetch attempt waits 30 seconds.
- The server CA cert is fetched only when a client is created.

## Observability

The extension logs to the client's `Logger` and emits metrics to the client's
`MetricsHandler`, if they are set in the client options before the client is
created.

Metrics are all labeled with `secret_type` (`api_key`, `tls_client_cert`,
`tls_client_key`, `tls_server_ca_cert`; a bundled cert/key secret is
`tls_client_cert`) and `fetch_path` (`extension` or `sdk`). Currently, the following metrics are emitted:

| Metric | Type | Extra labels | Description |
|---|---|---|---|
| `temporal_aws_secrets_manager_fetch_failed` | Counter | `failure_reason`, `fallback` (`stale`, `none`) | Failed fetches, and whether the previous value was used instead |
| `temporal_aws_secrets_manager_fetch_latency` | Timer | `outcome` (`success`, `failure`) | Latency of each fetch |
| `temporal_aws_secrets_manager_cache_lookup` | Counter | `result` (`hit`, `miss`, `miss_with_fallback`) | Lookups that returned a value |

## Required IAM permissions

The AWS credentials in use (for example, the Lambda execution role) need
`secretsmanager:GetSecretValue` on each configured secret:

```json
{
  "Effect": "Allow",
  "Action": "secretsmanager:GetSecretValue",
  "Resource": "arn:aws:secretsmanager:us-west-2:123456789012:secret:temporal/*"
}
```

If a secret is encrypted with a customer-managed KMS key, `kms:Decrypt` on that
key is also required.
