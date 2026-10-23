package secretsmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/temporal"
)

const (
	// ExtensionName is the name of the extension, which is also its table key under profile.<profile>.ext.
	ExtensionName = "aws_secrets_manager"

	pluginName = "go.temporal.io/sdk/contrib/aws/secretsmanager"

	envAPIKeyID       = "TEMPORAL_EXT_AWS_SECRETS_MANAGER_API_KEY_ID"
	envClientCertID   = "TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_CERT_ID"
	envClientKeyID    = "TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_CLIENT_KEY_ID"
	envServerCACertID = "TEMPORAL_EXT_AWS_SECRETS_MANAGER_TLS_SERVER_CA_CERT_ID"

	// Same env var and default as the AWS Parameters and Secrets Lambda Extension's cache TTL
	envSecretsManagerTTL   = "SECRETS_MANAGER_TTL"
	defaultRefreshInterval = 5 * time.Minute
	defaultMaxStaleness    = 24 * time.Hour
)

// SecretsClient is the subset of the AWS SDK v2 Secrets Manager client used to fetch secrets. It is implemented by
// [awssm.Client].
type SecretsClient interface {
	GetSecretValue(
		ctx context.Context,
		params *awssm.GetSecretValueInput,
		optFns ...func(*awssm.Options),
	) (*awssm.GetSecretValueOutput, error)
}

// Extension is an [envconfig.ClientConfigExtension] that loads client credentials from AWS Secrets Manager. The zero
// value is ready to use. See the package documentation for configuration details.
//
// NOTE: Experimental
type Extension struct {
	// Secrets Manager client used to fetch secrets. If nil, secrets are fetched through the AWS Parameters and Secrets
	// Lambda Extension if it is detected, and otherwise with a client created from the default AWS config. See the
	// package documentation for how the extension is detected.
	SecretsClient SecretsClient
	// How long a fetched secret is used before it is refreshed. If unset, defaults to the SECRETS_MANAGER_TTL env var
	// (non-negative whole seconds, where 0 refreshes on every use) as used by the AWS Parameters and Secrets Lambda
	// Extension, or 5 minutes if that is unset.
	RefreshInterval time.Duration
	// How long after its last successful fetch a secret may still be used if refreshing it fails. Defaults to 24 hours.
	// Values less than RefreshInterval are treated as RefreshInterval.
	MaxStaleness time.Duration
}

var _ envconfig.ClientConfigExtension = Extension{}

// Name implements [envconfig.ClientConfigExtension.Name].
func (Extension) Name() string { return ExtensionName }

// ClientPlugin implements [envconfig.ClientConfigExtension.ClientPlugin].
func (e Extension) ClientPlugin(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
	ids, err := loadSecretIDs(req)
	if err != nil {
		return nil, err
	}
	if ids == (secretIDs{}) {
		return nil, nil
	}

	refreshInterval, err := e.refreshInterval()
	if err != nil {
		return nil, err
	}
	maxStaleness := e.MaxStaleness
	if maxStaleness <= 0 {
		maxStaleness = defaultMaxStaleness
	}
	maxStaleness = max(maxStaleness, refreshInterval)

	var f fetcher
	if e.SecretsClient != nil {
		f = &sdkFetcher{client: e.SecretsClient}
	} else {
		f = newDefaultFetcher()
	}
	p := &plugin{ids: ids, cache: newSecretCache(f, refreshInterval, maxStaleness)}
	return temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name:            pluginName,
		ConfigureClient: p.configureClient,
	})
}

func (e Extension) refreshInterval() (time.Duration, error) {
	if e.RefreshInterval > 0 {
		return e.RefreshInterval, nil
	}
	// Read from the process env rather than the config env, like PARAMETERS_SECRETS_EXTENSION_HTTP_PORT, since this
	// is Lambda runtime config shared with the Lambda extension rather than Temporal client config
	s, ok := os.LookupEnv(envSecretsManagerTTL)
	if !ok || s == "" {
		return defaultRefreshInterval, nil
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil || secs < 0 || secs > int64(math.MaxInt64/time.Second) {
		return 0, fmt.Errorf("invalid %v %q, must be a non-negative whole number of seconds", envSecretsManagerTTL, s)
	}
	return time.Duration(secs) * time.Second, nil
}

type secretIDs struct {
	APIKey       string
	ClientCert   string
	ClientKey    string
	ServerCACert string
}

func (s secretIDs) hasTLS() bool {
	return s.ClientCert != "" || s.ClientKey != "" || s.ServerCACert != ""
}

func loadSecretIDs(req envconfig.ClientConfigExtensionRequest) (secretIDs, error) {
	var ids secretIDs
	if err := ids.applyTOML(req.Profile.Ext[ExtensionName]); err != nil {
		return secretIDs{}, err
	}
	if req.Env != nil {
		// Like core envconfig, set-but-empty env vars override (and so can unset) file values.
		for env, field := range map[string]*string{
			envAPIKeyID:       &ids.APIKey,
			envClientCertID:   &ids.ClientCert,
			envClientKeyID:    &ids.ClientKey,
			envServerCACertID: &ids.ServerCACert,
		} {
			if s, ok := req.Env.LookupEnv(env); ok {
				*field = s
			}
		}
	}
	if err := ids.validate(req.Profile); err != nil {
		return secretIDs{}, err
	}
	return ids, nil
}

func (s *secretIDs) applyTOML(table map[string]any) error {
	for key, val := range table {
		var err error
		switch key {
		case "api_key_id":
			s.APIKey, err = tomlString(key, val)
		case "tls":
			tlsTable, ok := val.(map[string]any)
			if !ok {
				return fmt.Errorf("ext.%v.tls must be a table, got %T", ExtensionName, val)
			}
			err = s.applyTLSTOML(tlsTable)
		default:
			err = fmt.Errorf("unknown key %q in ext.%v", key, ExtensionName)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *secretIDs) applyTLSTOML(table map[string]any) error {
	for key, val := range table {
		var err error
		switch key {
		case "client_cert_id":
			s.ClientCert, err = tomlString("tls."+key, val)
		case "client_key_id":
			s.ClientKey, err = tomlString("tls."+key, val)
		case "server_ca_cert_id":
			s.ServerCACert, err = tomlString("tls."+key, val)
		default:
			err = fmt.Errorf("unknown key %q in ext.%v.tls", key, ExtensionName)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func tomlString(key string, val any) (string, error) {
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("ext.%v.%v must be a string, got %T", ExtensionName, key, val)
	}
	return s, nil
}

func (s secretIDs) validate(prof *envconfig.ClientConfigProfile) error {
	var conflicts []string
	if s.APIKey != "" && prof.APIKey != "" {
		conflicts = append(conflicts, "API key secret ID cannot be set with an API key")
	}
	if prof.TLS != nil {
		if s.ClientCert != "" && (prof.TLS.ClientCertPath != "" || len(prof.TLS.ClientCertData) > 0) {
			conflicts = append(conflicts, "TLS client cert secret ID cannot be set with client cert path or data")
		}
		if s.ClientKey != "" && (prof.TLS.ClientKeyPath != "" || len(prof.TLS.ClientKeyData) > 0) {
			conflicts = append(conflicts, "TLS client key secret ID cannot be set with client key path or data")
		}
		if s.ServerCACert != "" && (prof.TLS.ServerCACertPath != "" || len(prof.TLS.ServerCACertData) > 0) {
			conflicts = append(conflicts, "TLS server CA cert secret ID cannot be set with server CA cert path or data")
		}
		if s.hasTLS() && prof.TLS.Disabled {
			conflicts = append(conflicts, "TLS secret IDs cannot be set when TLS is disabled")
		}
	}
	if (s.ClientCert == "") != (s.ClientKey == "") {
		conflicts = append(conflicts, "TLS client cert and key secret IDs must be set together")
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("invalid config: %v", conflicts)
	}
	return nil
}

type plugin struct {
	ids   secretIDs
	cache *secretCache
}

func (p *plugin) configureClient(ctx context.Context, options client.PluginConfigureClientOptions) error {
	opts := options.ClientOptions
	tel := telemetry{logger: opts.Logger, metrics: opts.MetricsHandler}
	if p.ids.APIKey != "" {
		if opts.Credentials != nil {
			return fmt.Errorf("cannot use API key from AWS Secrets Manager, client credentials already set")
		}
		// Fetch eagerly so that client creation fails on a bad secret instead of the first call
		if _, err := p.cache.get(ctx, p.ids.APIKey, secretTypeAPIKey, tel); err != nil {
			return err
		}
		opts.Credentials = client.NewAPIKeyDynamicCredentials(func(ctx context.Context) (string, error) {
			b, err := p.cache.get(ctx, p.ids.APIKey, secretTypeAPIKey, tel)
			return string(b), err
		})
		// Match envconfig, which defaults TLS to enabled when an API key is present
		if opts.ConnectionOptions.TLS == nil && !opts.ConnectionOptions.TLSDisabled {
			opts.ConnectionOptions.TLS = &tls.Config{}
		}
	}

	if !p.ids.hasTLS() {
		return nil
	}
	if opts.ConnectionOptions.TLSDisabled {
		return fmt.Errorf("cannot use TLS config from AWS Secrets Manager, TLS is disabled")
	}
	var conf *tls.Config
	if opts.ConnectionOptions.TLS != nil {
		conf = opts.ConnectionOptions.TLS.Clone()
	} else {
		conf = &tls.Config{}
	}

	if p.ids.ClientCert != "" {
		if len(conf.Certificates) > 0 || conf.GetClientCertificate != nil {
			return fmt.Errorf("cannot use TLS client cert from AWS Secrets Manager, client certificates already set")
		}
		// Fetch eagerly so that client creation fails on a bad secret instead of the first call
		if _, err := p.clientCert(ctx, tel); err != nil {
			return err
		}
		conf.GetClientCertificate = func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			ctx := info.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return p.clientCert(ctx, tel)
		}
	}

	if p.ids.ServerCACert != "" {
		if conf.RootCAs != nil {
			return fmt.Errorf("cannot use TLS server CA cert from AWS Secrets Manager, root CAs already set")
		}
		caPEM, err := p.cache.get(ctx, p.ids.ServerCACert, secretTypeServerCACert, tel)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return fmt.Errorf("no PEM certificates found in TLS server CA cert secret %q", p.ids.ServerCACert)
		}
		conf.RootCAs = pool
	}

	opts.ConnectionOptions.TLS = conf
	return nil
}

func (p *plugin) clientCert(ctx context.Context, tel telemetry) (*tls.Certificate, error) {
	certPEM, err := p.cache.get(ctx, p.ids.ClientCert, secretTypeClientCert, tel)
	if err != nil {
		return nil, err
	}
	// For a bundled cert/key secret, reuse the same value rather than getting it again, which could return a newer
	// value if the entry expired in between and mismatch the pair. X509KeyPair picks out the appropriate PEM blocks.
	keyPEM := certPEM
	if p.ids.ClientKey != p.ids.ClientCert {
		if keyPEM, err = p.cache.get(ctx, p.ids.ClientKey, secretTypeClientKey, tel); err != nil {
			return nil, err
		}
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed loading TLS client cert/key from secrets %q and %q: %w",
			p.ids.ClientCert, p.ids.ClientKey, err)
	}
	return &cert, nil
}
