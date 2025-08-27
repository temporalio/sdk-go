// Package envconfig contains utilities to load configuration from files and/or environment variables.
//
// WARNING: Environment configuration is currently experimental.
package envconfig

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// ClientConfig represents a client config file.
//
// WARNING: Environment configuration is currently experimental.
type ClientConfig struct {
	// Profiles, keyed by profile name.
	Profiles map[string]*ClientConfigProfile
}

// ClientConfigProfile is profile-level configuration for a client.
//
// WARNING: Environment configuration is currently experimental.
type ClientConfigProfile struct {
	// Client address.
	Address string
	// Client namespace.
	Namespace string
	// Client API key. If present and TLS field is nil or present but without Disabled as true, TLS is defaulted to
	// enabled.
	APIKey string
	// Optional client TLS config.
	TLS *ClientConfigTLS
	// Optional client codec config.
	Codec *ClientConfigCodec
	// GRPCAuthority will set the :authority pseudoheader on grpc clients.
	//
	// This is useful in situations where you might need to route through an intermediate proxy.
	GRPCAuthority string
	// Client gRPC metadata (aka headers). When loading from TOML and env var, or writing to TOML, the keys are
	// lowercased and hyphens are replaced with underscores. This is used for deduplicating/overriding too, so manually
	// set values that are not normalized may not get overridden with [ClientConfigProfile.ApplyEnvVars].
	GRPCMeta map[string]string
}

// ClientConfigTLS is TLS configuration for a client.
//
// WARNING: Environment configuration is currently experimental.
type ClientConfigTLS struct {
	// If true, TLS is explicitly disabled. If false/unset, whether TLS is enabled or not depends on other factors such
	// as whether this struct is present or nil, and whether API key exists (which enables TLS by default).
	Disabled bool
	// Path to client mTLS certificate. Mutually exclusive with ClientCertData.
	ClientCertPath string
	// PEM bytes for client mTLS certificate. Mutually exclusive with ClientCertPath.
	ClientCertData []byte
	// Path to client mTLS key. Mutually exclusive with ClientKeyData.
	ClientKeyPath string
	// PEM bytes for client mTLS key. Mutually exclusive with ClientKeyPath.
	ClientKeyData []byte
	// Path to server CA cert override. Mutually exclusive with ServerCACertData.
	ServerCACertPath string
	// PEM bytes for server CA cert override. Mutually exclusive with ServerCACertPath.
	ServerCACertData []byte
	// SNI override.
	ServerName string
	// True if host verification should be skipped.
	DisableHostVerification bool
}

// ClientConfigCodec is codec configuration for a client.
//
// WARNING: Environment configuration is currently experimental.
type ClientConfigCodec struct {
	// Remote endpoint for the codec.
	Endpoint string
	// Auth for the codec.
	Auth string
}

// ToClientOptionsRequest are options for [ClientConfig.ToClientOptions] and [ClientConfigProfile.ToClientOptions].
type ToClientOptionsRequest struct {
	// If true and a codec is configured, the data converter of the client will point to the codec remotely. Users
	// should usually not set this and rather configure the codec locally. Users should especially not enable this for
	// clients used by workers since they call the codec repeatedly even during workflow replay.
	IncludeRemoteCodec bool
}

// ToClientOptions converts the given profile to client options that can be used to create an SDK client. Defaults to
// "default" profile if profile is empty string. Will fail if profile not found.
func (c *ClientConfig) ToClientOptions(profile string, options ToClientOptionsRequest) (client.Options, error) {
	if profile == "" {
		profile = DefaultConfigFileProfile
	}
	prof, ok := c.Profiles[profile]
	if !ok {
		return client.Options{}, fmt.Errorf("profile not found")
	}
	return prof.ToClientOptions(options)
}

// ToClientOptions converts this profile to client options that can be used to create an SDK client.
func (c *ClientConfigProfile) ToClientOptions(options ToClientOptionsRequest) (client.Options, error) {
	opts := client.Options{
		HostPort:  c.Address,
		Namespace: c.Namespace,
	}
	if c.APIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(c.APIKey)
	}
	if c.TLS != nil {
		var err error
		if opts.ConnectionOptions.TLS, err = c.TLS.toTLSConfig(); err != nil {
			return client.Options{}, fmt.Errorf("invalid TLS config: %w", err)
		}
	} else if c.APIKey != "" && (c.TLS == nil || !c.TLS.Disabled) {
		opts.ConnectionOptions.TLS = &tls.Config{}
	}
	if c.Codec != nil && options.IncludeRemoteCodec {
		var err error
		if opts.DataConverter, err = c.Codec.toDataConverter(c.Namespace); err != nil {
			return client.Options{}, fmt.Errorf("invalid codec: %w", err)
		}
	}
	if c.GRPCAuthority != "" {
		opts.ConnectionOptions.Authority = c.GRPCAuthority
	}
	if len(c.GRPCMeta) > 0 {
		opts.HeadersProvider = fixedHeaders(c.GRPCMeta)
	}
	return opts, nil
}

func (c *ClientConfigTLS) toTLSConfig() (*tls.Config, error) {
	if c.Disabled {
		return nil, nil
	}
	conf := &tls.Config{}

	// Client cert
	if len(c.ClientCertData) > 0 || len(c.ClientKeyData) > 0 {
		if len(c.ClientCertData) == 0 || len(c.ClientKeyData) == 0 {
			return nil, fmt.Errorf("if either client cert or key data is present, other must be present too")
		} else if c.ClientCertPath != "" || c.ClientKeyPath != "" {
			return nil, fmt.Errorf("cannot have client key/cert path with data")
		}
		cert, err := tls.X509KeyPair(c.ClientCertData, c.ClientKeyData)
		if err != nil {
			return nil, fmt.Errorf("failed loading client cert/key data: %w", err)
		}
		conf.Certificates = append(conf.Certificates, cert)
	} else if c.ClientCertPath != "" || c.ClientKeyPath != "" {
		if c.ClientCertPath == "" || c.ClientKeyPath == "" {
			return nil, fmt.Errorf("if either client cert or key path is present, other must be present too")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCertPath, c.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed loading client cert/key path: %w", err)
		}
		conf.Certificates = append(conf.Certificates, cert)
	}

	// Server CA cert
	if len(c.ServerCACertData) > 0 || c.ServerCACertPath != "" {
		pool := x509.NewCertPool()
		serverCAData := c.ServerCACertData
		if len(serverCAData) == 0 {
			var err error
			if serverCAData, err = os.ReadFile(c.ServerCACertPath); err != nil {
				return nil, fmt.Errorf("failed reading server CA cert path: %w", err)
			}
		} else if c.ServerCACertPath == "" {
			return nil, fmt.Errorf("cannot have server CA cert path with data")
		}
		if !pool.AppendCertsFromPEM(serverCAData) {
			return nil, fmt.Errorf("failed adding server CA to CA pool")
		}
		conf.RootCAs = pool
	}

	conf.ServerName = c.ServerName
	conf.InsecureSkipVerify = c.DisableHostVerification
	return conf, nil
}

func (c *ClientConfigCodec) toDataConverter(namespace string) (converter.DataConverter, error) {
	return converter.NewRemoteDataConverter(converter.GetDefaultDataConverter(), converter.RemoteDataConverterOptions{
		Endpoint: c.Endpoint,
		ModifyRequest: func(req *http.Request) error {
			req.Header.Set("X-Namespace", namespace)
			if c.Auth != "" {
				req.Header.Set("Authorization", c.Auth)
			}
			return nil
		},
	}), nil
}

// NormalizeGRPCMetaKey converts the given key to lowercase and replaces underscores with hyphens.
func NormalizeGRPCMetaKey(k string) string {
	return strings.ToLower(strings.ReplaceAll(k, "_", "-"))
}

type fixedHeaders map[string]string

func (f fixedHeaders) GetHeaders(context.Context) (map[string]string, error) { return f, nil }
