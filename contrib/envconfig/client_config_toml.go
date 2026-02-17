package envconfig

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// knownProfileKeys contains the TOML keys recognized for profile configuration.
// This must be kept in sync with tomlClientConfigProfile's TOML tags.
// See TestKnownProfileKeysInSync for validation.
var knownProfileKeys = map[string]bool{
	"address":   true,
	"namespace": true,
	"api_key":   true,
	"tls":       true,
	"codec":     true,
	"grpc_meta": true,
}

// ClientConfigToTOMLOptions are options for [ClientConfig.ToTOML].
type ClientConfigToTOMLOptions struct {
	// Defaults to two-space indent.
	OverrideIndent *string
	// If non-nil, these additional fields will be serialized with each profile.
	// Key is profile name, value is map of field name to field value.
	AdditionalProfileFields map[string]map[string]any
}

// ToTOML converts the client config to TOML. Note, this may not be byte-for-byte exactly what may have been set in
// [ClientConfig.FromTOML].
func (c *ClientConfig) ToTOML(options ClientConfigToTOMLOptions) ([]byte, error) {
	var conf tomlClientConfig
	conf.fromClientConfig(c)

	// Encode to TOML then decode to map for merging additional fields
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(&conf); err != nil {
		return nil, err
	}
	var rawConf map[string]any
	if _, err := toml.Decode(buf.String(), &rawConf); err != nil {
		return nil, err
	}

	// Merge additional fields into profiles
	for profileName, additional := range options.AdditionalProfileFields {
		profiles, _ := rawConf["profile"].(map[string]any)
		if profiles == nil {
			profiles = make(map[string]any)
			rawConf["profile"] = profiles
		}
		profile, _ := profiles[profileName].(map[string]any)
		if profile == nil {
			profile = make(map[string]any)
			profiles[profileName] = profile
		}
		for k, v := range additional {
			if knownProfileKeys[k] {
				return nil, fmt.Errorf("additional field %q in profile %q conflicts with known profile field", k, profileName)
			}
			profile[k] = v
		}
	}

	// Re-encode with merged data
	var out bytes.Buffer
	enc := toml.NewEncoder(&out)
	if options.OverrideIndent != nil {
		enc.Indent = *options.OverrideIndent
	}
	if err := enc.Encode(rawConf); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type ClientConfigFromTOMLOptions struct {
	// If true, will error if there are unrecognized keys.
	Strict bool
	// If non-nil, populated with additional (unrecognized) profile fields.
	// Key is profile name, value is map of field name to field value.
	// This allows callers to preserve custom profile fields without modifying
	// this package. Note, if Strict is true the additional fields will cause an
	// error before they can be captured here.
	AdditionalProfileFields map[string]map[string]any
}

// FromTOML converts from TOML to the client config. This will replace all profiles within, it does not do any form of
// merging.
func (c *ClientConfig) FromTOML(b []byte, options ClientConfigFromTOMLOptions) error {
	var conf tomlClientConfig
	md, err := toml.Decode(string(b), &conf)
	if err != nil {
		return err
	}

	undecoded := md.Undecoded()
	if options.Strict && len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return fmt.Errorf("key(s) unrecognized: %v", strings.Join(keys, ", "))
	}

	// If AdditionalProfileFields is requested, extract unknown profile fields.
	if options.AdditionalProfileFields != nil && len(undecoded) > 0 {
		// Decode again into raw map to get additional field values
		var rawConf struct {
			Profiles map[string]map[string]any `toml:"profile"`
		}
		if _, err := toml.Decode(string(b), &rawConf); err != nil {
			return err
		}

		for _, key := range undecoded {
			// Skip non-profile undecoded keys (e.g., unknown top-level sections)
			if len(key) < 3 || key[0] != "profile" {
				continue
			}
			profileName := key[1]
			fieldKey := key[2]
			if v, ok := rawConf.Profiles[profileName][fieldKey]; ok {
				if options.AdditionalProfileFields[profileName] == nil {
					options.AdditionalProfileFields[profileName] = make(map[string]any)
				}
				options.AdditionalProfileFields[profileName][fieldKey] = v
			}
		}
	}

	conf.applyToClientConfig(c)
	return nil
}

type tomlClientConfig struct {
	Profiles map[string]tomlClientConfigProfile `toml:"profile"`
}

func (c *tomlClientConfig) applyToClientConfig(conf *ClientConfig) {
	conf.Profiles = make(map[string]*ClientConfigProfile, len(c.Profiles))
	for k, v := range c.Profiles {
		conf.Profiles[k] = v.toClientConfig()
	}
}

func (c *tomlClientConfig) fromClientConfig(conf *ClientConfig) {
	c.Profiles = make(map[string]tomlClientConfigProfile, len(conf.Profiles))
	for k, v := range conf.Profiles {
		var prof tomlClientConfigProfile
		prof.fromClientConfig(v)
		c.Profiles[k] = prof
	}
}

type tomlClientConfigProfile struct {
	Address   string                 `toml:"address,omitempty"`
	Namespace string                 `toml:"namespace,omitempty"`
	APIKey    string                 `toml:"api_key,omitempty"`
	TLS       *tomlClientConfigTLS   `toml:"tls,omitempty"`
	Codec     *tomlClientConfigCodec `toml:"codec,omitempty"`
	GRPCMeta  map[string]string      `toml:"grpc_meta,omitempty"`
}

func (c *tomlClientConfigProfile) toClientConfig() *ClientConfigProfile {
	ret := &ClientConfigProfile{
		Address:   c.Address,
		Namespace: c.Namespace,
		APIKey:    c.APIKey,
		TLS:       c.TLS.toClientConfig(),
		Codec:     c.Codec.toClientConfig(),
	}
	// gRPC meta keys have to be normalized
	if len(c.GRPCMeta) > 0 {
		ret.GRPCMeta = make(map[string]string, len(c.GRPCMeta))
		for k, v := range c.GRPCMeta {
			ret.GRPCMeta[NormalizeGRPCMetaKey(k)] = v
		}
	}
	return ret
}

func (c *tomlClientConfigProfile) fromClientConfig(conf *ClientConfigProfile) {
	c.Address = conf.Address
	c.Namespace = conf.Namespace
	c.APIKey = conf.APIKey
	if conf.TLS != nil {
		c.TLS = &tomlClientConfigTLS{}
		c.TLS.fromClientConfig(conf.TLS)
	}
	if conf.Codec != nil {
		c.Codec = &tomlClientConfigCodec{}
		c.Codec.fromClientConfig(conf.Codec)
	}
	// gRPC meta keys have to be normalized (we can mutate receiver, it's only used ephemerally)
	if len(conf.GRPCMeta) > 0 {
		c.GRPCMeta = make(map[string]string, len(conf.GRPCMeta))
		for k, v := range conf.GRPCMeta {
			c.GRPCMeta[NormalizeGRPCMetaKey(k)] = v
		}
	}
}

type tomlClientConfigTLS struct {
	Disabled                bool   `toml:"disabled,omitempty"`
	ClientCertPath          string `toml:"client_cert_path,omitempty"`
	ClientCertData          string `toml:"client_cert_data,omitempty"`
	ClientKeyPath           string `toml:"client_key_path,omitempty"`
	ClientKeyData           string `toml:"client_key_data,omitempty"`
	ServerCACertPath        string `toml:"server_ca_cert_path,omitempty"`
	ServerCACertData        string `toml:"server_ca_cert_data,omitempty"`
	ServerName              string `toml:"server_name,omitempty"`
	DisableHostVerification bool   `toml:"disable_host_verification,omitempty"`
}

func (c *tomlClientConfigTLS) toClientConfig() *ClientConfigTLS {
	if c == nil {
		return nil
	}
	// For deep equality, we want empty strings as nil byte slices, not empty byte slices
	var certData, keyData, caData []byte
	if c.ClientCertData != "" {
		certData = []byte(c.ClientCertData)
	}
	if c.ClientKeyData != "" {
		keyData = []byte(c.ClientKeyData)
	}
	if c.ServerCACertData != "" {
		caData = []byte(c.ServerCACertData)
	}
	return &ClientConfigTLS{
		Disabled:                c.Disabled,
		ClientCertPath:          c.ClientCertPath,
		ClientCertData:          certData,
		ClientKeyPath:           c.ClientKeyPath,
		ClientKeyData:           keyData,
		ServerCACertPath:        c.ServerCACertPath,
		ServerCACertData:        caData,
		ServerName:              c.ServerName,
		DisableHostVerification: c.DisableHostVerification,
	}
}

func (c *tomlClientConfigTLS) fromClientConfig(conf *ClientConfigTLS) {
	c.Disabled = conf.Disabled
	c.ClientCertPath = conf.ClientCertPath
	c.ClientCertData = string(conf.ClientCertData)
	c.ClientKeyPath = conf.ClientKeyPath
	c.ClientKeyData = string(conf.ClientKeyData)
	c.ServerCACertPath = conf.ServerCACertPath
	c.ServerCACertData = string(conf.ServerCACertData)
	c.ServerName = conf.ServerName
	c.DisableHostVerification = conf.DisableHostVerification
}

type tomlClientConfigCodec struct {
	Endpoint string `toml:"endpoint,omitempty"`
	Auth     string `toml:"auth,omitempty"`
}

func (c *tomlClientConfigCodec) toClientConfig() *ClientConfigCodec {
	if c == nil {
		return nil
	}
	return &ClientConfigCodec{Endpoint: c.Endpoint, Auth: c.Auth}
}

func (c *tomlClientConfigCodec) fromClientConfig(conf *ClientConfigCodec) {
	c.Endpoint = conf.Endpoint
	c.Auth = conf.Auth
}
