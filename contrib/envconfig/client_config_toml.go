package envconfig

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

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

	if len(options.AdditionalProfileFields) > 0 {
		// Look for recognized fields (at any level) in AdditionalProfileFields
		buf.Reset()
		if err := toml.NewEncoder(&buf).Encode(map[string]any{"profile": options.AdditionalProfileFields}); err != nil {
			return nil, err
		}

		// also re-parse the additional fields as raw to normalize types and match what's in rawConf
		var rawAdditional map[string]any
		_, err := toml.Decode(buf.String(), &rawAdditional)
		if err != nil {
			return nil, err
		}

		var additionalConf tomlClientConfig
		additionalMD, err := toml.Decode(buf.String(), &additionalConf)
		if err != nil {
			return nil, err
		}
		additionalKeys := additionalMD.Keys()
		conflicts := make(map[string]toml.Key, len(additionalKeys))

		// Assume everything is a conflict until proven otherwise
		for _, key := range additionalKeys {
			conflicts[key.String()] = key
		}

		// Undecodable fields are exactly what we want to merge back in
		for _, key := range additionalMD.Undecoded() {
			delete(conflicts, key.String())
		}

		// If there's an additional *nested* field, like profile.default.tls.future, its parent (profile.default.tls) will
		// show up in keys. As long as that key is a table (or a list of tables), it's not a conflict.
		for keyName, key := range conflicts {
			keyType := additionalMD.Type(key...)
			if keyType == "Hash" || keyType == "ArrayHash" {
				delete(conflicts, keyName)
			}
		}

		if len(conflicts) > 0 {
			names := slices.Sorted(maps.Keys(conflicts))
			return nil, fmt.Errorf("additional field(s) %s conflict with known profile fields", strings.Join(names, ", "))
		}

		for _, key := range additionalMD.Undecoded() {
			copyConfig(rawAdditional, rawConf, key)
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
	// Any existing values will be deleted.
	// This allows callers to preserve custom profile fields without modifying
	// this package. Note, if Strict is true the additional fields will cause an
	// error before they can be captured here.
	AdditionalProfileFields map[string]map[string]any
}

// FromTOML converts from TOML to the client config. This will replace all profiles within, it does not do any form of
// merging.
func (c *ClientConfig) FromTOML(b []byte, options ClientConfigFromTOMLOptions) error {
	var conf tomlClientConfig
	clear(options.AdditionalProfileFields)
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
			if options.AdditionalProfileFields[profileName] == nil {
				options.AdditionalProfileFields[profileName] = make(map[string]any)
			}
			copyConfig(rawConf.Profiles[profileName], options.AdditionalProfileFields[profileName], key[2:])
		}
	}

	conf.applyToClientConfig(c)
	return nil
}

// copyConfig copies the value of key from source to dest, ensuring the appropriate
// maps and slices in the hierarchy are created. If non-empty, source and dest should
// be populated via toml.Decode.
func copyConfig(source, dest map[string]any, key toml.Key) {
	for i, keyPart := range key {
		var nextSource, nextDest map[string]any

		switch sourceVal := source[keyPart].(type) {
		case map[string]any:
			nextSource = sourceVal
			nextDest, _ = dest[keyPart].(map[string]any)
			if nextDest == nil {
				nextDest = make(map[string]any)
				dest[keyPart] = nextDest
			}
			// no need to do anything more, subkeys will be copied as necessary
		case []map[string]any:
			destVal, _ := dest[keyPart].([]map[string]any)
			if destVal == nil {
				destVal = make([]map[string]any, 0, len(sourceVal))
				dest[keyPart] = destVal
			}

			if i == len(key)-1 {
				// seeing the list name starts a fresh map
				destVal = append(destVal, make(map[string]any))
				dest[keyPart] = destVal
			} else {
				// TOML doesn't allow list entries to be defined before the list itself is
				// defined. We can trust that there will always be at least one entry by the
				// time this branch is reached.
				pos := len(destVal) - 1
				nextDest = destVal[pos]
				nextSource = sourceVal[pos]
			}
		default:
			// everything else gets copied over
			dest[keyPart] = sourceVal
			return
		}
		source = nextSource
		dest = nextDest
	}
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
	Authority string                 `toml:"authority,omitempty"`
}

func (c *tomlClientConfigProfile) toClientConfig() *ClientConfigProfile {
	ret := &ClientConfigProfile{
		Address:   c.Address,
		Namespace: c.Namespace,
		APIKey:    c.APIKey,
		TLS:       c.TLS.toClientConfig(),
		Codec:     c.Codec.toClientConfig(),
		Authority: c.Authority,
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
	c.Authority = conf.Authority
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
