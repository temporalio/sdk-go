// Package secretsmanager provides an [go.temporal.io/sdk/contrib/envconfig.ClientConfigExtension] that loads Temporal
// client credentials (API key and/or TLS material) from AWS Secrets Manager, so that only secret IDs, never secret
// material, need to live in static config.
//
// # Configuration
//
// Each credential is configured with a Secrets Manager secret ID (name or full ARN), either in the TOML config file:
//
//	[profile.default.ext.aws_secrets_manager]
//	api_key_id = "my-temporal-api-key"
//
//	[profile.default.ext.aws_secrets_manager.tls]
//	client_cert_id = "my-temporal-client-cert"
//	client_key_id = "my-temporal-client-key"
//	server_ca_cert_id = "my-temporal-server-ca-cert"
//
// or with environment variables, which take precedence over the file:
//
//	TEMPORAL_EXT_AWS_SECRETS_MANAGER_API_KEY_ID
//	TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_CERT_ID
//	TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_KEY_ID
//	TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_SERVER_CA_CERT_ID
//
// A secret ID cannot be combined with its Temporal-native counterpart (e.g. api_key / TEMPORAL_API_KEY, or
// client_cert_path / client_cert_data), from any source. The client cert and key IDs must be set together. They may
// be the same ID, in which case the secret must contain both the PEM certificate and key, which allows the pair to be
// rotated atomically.
//
// Secret values are used as-is: the API key as a string, and TLS material as PEM.
//
// # Usage
//
//	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
//		Extensions: []envconfig.ClientConfigExtension{secretsmanager.Extension{}},
//	})
//
// # Fetching and caching
//
// By default, secrets are fetched through the AWS Parameters and Secrets Lambda Extension when it is detected, and
// otherwise directly from Secrets Manager using an AWS SDK client created from the default AWS config. Set
// [Extension.SecretsClient] to use a specific client instead. The Lambda extension is used on the port in the
// PARAMETERS_SECRETS_EXTENSION_HTTP_PORT environment variable if it is set, or, if it is unset, on the extension's
// default port (2773) if something is listening there when options are loaded. If another service uses port 2773, set
// PARAMETERS_SECRETS_EXTENSION_HTTP_PORT to an empty value to disable detection and always fetch directly.
//
// Secrets are first fetched when a client is created from the loaded options, failing client creation if a fetch
// fails. Fetched secrets are cached by the plugin returned from the extension, so clients created from the same
// loaded options share them. The API key and client cert/key are refreshed during the lifetime of a client once
// [Extension.RefreshInterval] (which defaults from the SECRETS_MANAGER_TTL env var, or 5 minutes if unset, like the
// Lambda extension) elapses, so rotated secrets are picked up without recreating the client. If a refresh fails,
// the previous value continues to be used for up to [Extension.MaxStaleness]. The server CA cert is only fetched when
// a client is created.
//
// # Observability
//
// Fetches, rotations, fallbacks to stale values, and recoveries are logged to [client.Options.Logger], and metrics
// are emitted to [client.Options.MetricsHandler], if they are set in the client options before the client is created.
// Secret material is never logged or included in metrics. The metrics, all labeled with secret_type and fetch_path,
// are:
//
//	temporal_aws_secrets_manager_fetch_failed   (counter, also labeled with failure_reason and fallback)
//	temporal_aws_secrets_manager_fetch_latency  (timer, also labeled with outcome)
//	temporal_aws_secrets_manager_cache_lookup   (counter, also labeled with result)
//
// The AWS credentials in use must allow secretsmanager:GetSecretValue on the configured secrets.
// If a secret is encrypted with a customer-managed KMS key, `kms:Decrypt` on that key is also required.
//
// NOTE: Experimental
package secretsmanager
