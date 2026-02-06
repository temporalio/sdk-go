package envconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/client"
)

// MustLoadDefaultClientOptions invokes [LoadDefaultClientOptions] and panics on error.
func MustLoadDefaultClientOptions() client.Options {
	c, err := LoadDefaultClientOptions()
	if err != nil {
		panic(err)
	}
	return c
}

// LoadDefaultClientOptions loads client options using default information from config files and environment variables.
// This just delegates to [LoadClientOptions] with the default options set. See that function and associated options for
// where/how values are loaded.
func LoadDefaultClientOptions() (client.Options, error) {
	return LoadClientOptions(LoadClientOptionsRequest{})
}

// LoadClientOptionsRequest are options for [LoadClientOptions].
type LoadClientOptionsRequest struct {
	// Override the file path to use to load the TOML file for config. Defaults to TEMPORAL_CONFIG_FILE environment
	// variable or if that is unset/empty, defaults to [os.UserConfigDir]/temporalio/temporal.toml. If ConfigFileData is
	// set, this cannot be set and no file loading from disk occurs. Ignored if DisableFile is true.
	ConfigFilePath string

	// TOML data to load for config. If set, this overrides any file loading. Cannot be set if ConfigFilePath is set.
	// Ignored if DisableFile is true.
	ConfigFileData []byte

	// Specific profile to use after file is loaded. Defaults to TEMPORAL_PROFILE environment variable or if that is
	// unset/empty, defaults to "default". If either this or the environment variable are set, load will fail if the
	// profile isn't present in the config. Ignored if DisableFile is true.
	ConfigFileProfile string

	// If true, will error if there are unrecognized keys.
	ConfigFileStrict bool

	// If true, will not do any TOML loading from file or data. This and DisableEnv cannot both be true.
	DisableFile bool

	// If true, will not apply environment variables on top of file config for the client options, but
	// TEMPORAL_CONFIG_FILE and TEMPORAL_PROFILE environment variables may still by used to populate defaults in this
	// options structure.
	DisableEnv bool

	// If true and a codec is configured, the data converter of the client will point to the codec remotely. Users
	// should usually not set this and rather configure the codec locally. Users should especially not enable this for
	// clients used by workers since they call the codec repeatedly even during workflow replay.
	IncludeRemoteCodec bool

	// Override the environment variable lookup. If nil, defaults to [EnvLookupOS].
	EnvLookup EnvLookup
}

// LoadClientOptions loads client options from file and then applies environment variable overrides. This will not fail
// if the config file does not exist. This is effectively a shortcut for [LoadClientConfigProfile] +
// [ClientConfigProfile.ToClientOptions]. See [LoadClientOptionsRequest] and [ClientConfigProfile] on how files and
// environment variables are applied.
func LoadClientOptions(options LoadClientOptionsRequest) (client.Options, error) {
	// Load profile
	prof, err := LoadClientConfigProfile(LoadClientConfigProfileOptions{
		ConfigFilePath:    options.ConfigFilePath,
		ConfigFileData:    options.ConfigFileData,
		ConfigFileProfile: options.ConfigFileProfile,
		ConfigFileStrict:  options.ConfigFileStrict,
		DisableFile:       options.DisableFile,
		DisableEnv:        options.DisableEnv,
		EnvLookup:         options.EnvLookup,
	})
	if err != nil {
		return client.Options{}, err
	}

	// Convert to client options
	return prof.ToClientOptions(ToClientOptionsRequest{IncludeRemoteCodec: options.IncludeRemoteCodec})
}

// [LoadClientConfigOptions] are options for [LoadClientConfig].
type LoadClientConfigOptions struct {
	// Override the file path to use to load the TOML file for config. Defaults to TEMPORAL_CONFIG_FILE environment
	// variable or if that is unset/empty, defaults to [os.UserConfigDir]/temporalio/temporal.toml. If ConfigFileData is
	// set, this cannot be set and no file loading from disk occurs.
	ConfigFilePath string

	// TOML data to load for config. If set, this overrides any file loading. Cannot be set if ConfigFilePath is set.
	ConfigFileData []byte

	// If true, will error if there are unrecognized keys.
	ConfigFileStrict bool

	// Override the environment variable lookup (only used to determine which config file to load). If nil,
	// defaults to [EnvLookupOS].
	EnvLookup EnvLookup
}

// LoadClientConfig loads the client configuration structure from TOML. Does not load values from environment variables
// (but may use environment variables to get which config file to load). This will not fail if the file does not exist.
// See [ClientConfig.FromTOML] for details on format.
func LoadClientConfig(options LoadClientConfigOptions) (ClientConfig, error) {
	var conf ClientConfig
	// Get which bytes to load from TOML
	var data []byte
	if len(options.ConfigFileData) > 0 {
		if options.ConfigFilePath != "" {
			return ClientConfig{}, fmt.Errorf("cannot have data and file path")
		}
		data = options.ConfigFileData
	} else {
		// Get file name which is either set value, env var, or default path
		file := options.ConfigFilePath
		if file == "" {
			env := options.EnvLookup
			if env == nil {
				env = EnvLookupOS
			}
			// Unlike env vars for the config values, empty and unset env var
			// for config file path are both treated as unset
			file, _ = env.LookupEnv("TEMPORAL_CONFIG_FILE")
		}
		if file == "" {
			var err error
			if file, err = DefaultConfigFilePath(); err != nil {
				return ClientConfig{}, err
			}
		}
		// Load file, not exist is ok
		if b, err := os.ReadFile(file); err == nil {
			data = b
		} else if !os.IsNotExist(err) {
			return ClientConfig{}, fmt.Errorf("failed reading file at %v: %w", file, err)
		}
	}

	// Parse data
	if err := conf.FromTOML(data, ClientConfigFromTOMLOptions{Strict: options.ConfigFileStrict}); err != nil {
		return ClientConfig{}, fmt.Errorf("failed parsing config: %w", err)
	}
	return conf, nil
}

// LoadClientConfigProfileOptions are options for [LoadClientConfigProfile].
type LoadClientConfigProfileOptions struct {
	// Override the file path to use to load the TOML file for config. Defaults to TEMPORAL_CONFIG_FILE environment
	// variable or if that is unset/empty, defaults to [os.UserConfigDir]/temporalio/temporal.toml. If ConfigFileData is
	// set, this cannot be set and no file loading from disk occurs. Ignored if DisableFile is true.
	ConfigFilePath string

	// TOML data to load for config. If set, this overrides any file loading. Cannot be set if ConfigFilePath is set.
	// Ignored if DisableFile is true.
	ConfigFileData []byte

	// Specific profile to use after file is loaded. Defaults to TEMPORAL_PROFILE environment variable or if that is
	// unset/empty, defaults to "default". If either this or the environment variable are set, load will fail if the
	// profile isn't present in the config. Ignored if DisableFile is true.
	ConfigFileProfile string

	// If true, will error if there are unrecognized keys.
	ConfigFileStrict bool

	// If true, will not do any TOML loading from file or data. This and DisableEnv cannot both be true.
	DisableFile bool

	// If true, will not apply environment variables on top of file config for the client options, but
	// TEMPORAL_CONFIG_FILE and TEMPORAL_PROFILE environment variables may still by used to populate defaults in this
	// options structure.
	DisableEnv bool

	// Override the environment variable lookup. If nil, defaults to [EnvLookupOS].
	EnvLookup EnvLookup
}

// LoadClientConfigProfile loads a specific client config profile from file and then applies environment variable
// overrides. This will not fail if the config file does not exist. This is effectively a shortcut for
// [LoadClientConfig] + [ClientConfigProfile.ApplyEnvVars]. See [LoadClientOptionsRequest] and [ClientConfigProfile] on
// how files and environment variables are applied.
func LoadClientConfigProfile(options LoadClientConfigProfileOptions) (ClientConfigProfile, error) {
	if options.DisableFile && options.DisableEnv {
		return ClientConfigProfile{}, fmt.Errorf("cannot disable file and env")
	}

	var prof ClientConfigProfile

	// If file is enabled, load it and find just the profile
	if !options.DisableFile {
		// Load
		conf, err := LoadClientConfig(LoadClientConfigOptions{
			ConfigFilePath:   options.ConfigFilePath,
			ConfigFileData:   options.ConfigFileData,
			ConfigFileStrict: options.ConfigFileStrict,
			EnvLookup:        options.EnvLookup,
		})
		if err != nil {
			return ClientConfigProfile{}, err
		}
		// Find user-set profile or use the default. Only fail if the profile was set and not found (if unset and not
		// found, that's ok).
		profile := options.ConfigFileProfile
		profileUnset := false
		if profile == "" {
			env := options.EnvLookup
			if env == nil {
				env = EnvLookupOS
			}
			// Unlike env vars for the config values, empty and unset env var
			// for config file path are both treated as unset
			profile, _ = env.LookupEnv("TEMPORAL_PROFILE")
		}
		if profile == "" {
			profile = DefaultConfigFileProfile
			profileUnset = true
		}
		if profPtr := conf.Profiles[profile]; profPtr != nil {
			prof = *profPtr
		} else if !profileUnset {
			return ClientConfigProfile{}, fmt.Errorf("unable to find profile %v in config data", profile)
		}
	}

	// If env is enabled, apply it
	if !options.DisableEnv {
		if err := prof.ApplyEnvVars(options.EnvLookup); err != nil {
			return ClientConfigProfile{}, fmt.Errorf("unable to apply env vars: %w", err)
		}
	}

	return prof, nil
}

// DefaultConfigFileProfile is the default profile used.
const DefaultConfigFileProfile = "default"

// DefaultConfigFilePath is the default config file path used. It is [os.UserConfigDir]/temporalio/temporal.toml.
func DefaultConfigFilePath() (string, error) {
	userDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed getting user config dir: %w", err)
	}
	return filepath.Join(userDir, "temporalio", "temporal.toml"), nil
}

// EnvLookup abstracts environment variable lookup for [ClientConfigProfile.ApplyEnvVars]. [EnvLookupOS] is the common
// implementation.
type EnvLookup interface {
	// Environ gets all environment variables in the same manner as [os.Environ].
	Environ() []string
	// Getenv gets a single environment variable in the same manner as [os.LookupEnv].
	LookupEnv(string) (string, bool)
}

type envLookupOS struct{}

// EnvLookupOS implements [EnvLookup] for [os].
var EnvLookupOS EnvLookup = envLookupOS{}

func (envLookupOS) Environ() []string                   { return os.Environ() }
func (envLookupOS) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// ApplyEnvVars overwrites any values in the profile with environment variables if the environment variables are set and
// non-empty. If env lookup is nil, defaults to [EnvLookupOS]
func (c *ClientConfigProfile) ApplyEnvVars(env EnvLookup) error {
	if env == nil {
		env = EnvLookupOS
	}
	if s, ok := env.LookupEnv("TEMPORAL_ADDRESS"); ok {
		c.Address = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_NAMESPACE"); ok {
		c.Namespace = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_API_KEY"); ok {
		c.APIKey = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS"); ok {
		if v, ok := envVarToBool(s); ok {
			if c.TLS == nil {
				c.TLS = &ClientConfigTLS{}
			}
			c.TLS.Disabled = !v
		}
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_CLIENT_CERT_PATH"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ClientCertPath = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_CLIENT_CERT_DATA"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ClientCertData = []byte(s)
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_CLIENT_KEY_PATH"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ClientKeyPath = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_CLIENT_KEY_DATA"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ClientKeyData = []byte(s)
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_SERVER_CA_CERT_PATH"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ServerCACertPath = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_SERVER_CA_CERT_DATA"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ServerCACertData = []byte(s)
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_SERVER_NAME"); ok {
		if c.TLS == nil {
			c.TLS = &ClientConfigTLS{}
		}
		c.TLS.ServerName = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_TLS_DISABLE_HOST_VERIFICATION"); ok {
		if v, ok := envVarToBool(s); ok {
			if c.TLS == nil {
				c.TLS = &ClientConfigTLS{}
			}
			c.TLS.DisableHostVerification = v
		}
	}
	if s, ok := env.LookupEnv("TEMPORAL_CODEC_ENDPOINT"); ok {
		if c.Codec == nil {
			c.Codec = &ClientConfigCodec{}
		}
		c.Codec.Endpoint = s
	}
	if s, ok := env.LookupEnv("TEMPORAL_CODEC_AUTH"); ok {
		if c.Codec == nil {
			c.Codec = &ClientConfigCodec{}
		}
		c.Codec.Auth = s
	}

	// GRPC meta requires crawling the envs to find
	for _, v := range env.Environ() {
		if strings.HasPrefix(v, "TEMPORAL_GRPC_META_") {
			pieces := strings.SplitN(v, "=", 2)
			if c.GRPCMeta == nil {
				c.GRPCMeta = map[string]string{}
			}
			// Keys have to be normalized
			key := NormalizeGRPCMetaKey(strings.TrimPrefix(pieces[0], "TEMPORAL_GRPC_META_"))
			// Empty env vars are not the same as unset. Unset will leave the
			// meta key unchanged, but empty removes it.
			if pieces[1] == "" {
				delete(c.GRPCMeta, key)
			} else {
				c.GRPCMeta[key] = pieces[1]
			}
		}
	}
	return nil
}

func envVarToBool(val string) (v bool, ok bool) {
	val = strings.ToLower(val)
	return val == "1" || val == "true", val == "1" || val == "0" || val == "true" || val == "false"
}
