package envconfig

import (
	"fmt"

	"go.temporal.io/sdk/client"
)

// ClientConfigExtension takes existing config and emits a plugin to apply additional config to the client.
// Extensions are passed to [LoadClientOptions] via [LoadClientOptionsRequest.Extensions].
//
// An extension's TOML config lives in the profile.<profile>.ext.<name> table, available as
// [ClientConfigProfile.Ext][name]. Extensions are recommended to use a TEMPORAL_EXT_<NAME>_ prefix for their env vars.
//
// NOTE: Experimental
type ClientConfigExtension interface {
	// Name is the extension's table key under profile.<profile>.ext. Must be non-empty and unique among loaded
	// extensions.
	Name() string
	// ClientPlugin reads this extension's config from the profile and env, and returns a plugin that applies it to
	// the client, or nil if there's nothing to apply.
	ClientPlugin(req ClientConfigExtensionRequest) (client.Plugin, error)
}

// ClientConfigExtensionRequest are options for [ClientConfigExtension.ClientPlugin].
type ClientConfigExtensionRequest struct {
	// Profile that was loaded, after env vars have been applied (unless disabled). Must not be mutated.
	Profile *ClientConfigProfile
	// Env to read extension env vars from, or nil if env loading is disabled.
	Env EnvLookup
}

func validateExtensions(extensions []ClientConfigExtension) error {
	seen := make(map[string]bool, len(extensions))
	for i, ext := range extensions {
		if ext == nil {
			return fmt.Errorf("client config extension at index %d is nil", i)
		}
		name := ext.Name()
		if name == "" {
			return fmt.Errorf("client config extension at index %d (%T) has empty name", i, ext)
		} else if seen[name] {
			return fmt.Errorf("duplicate client config extension name %q", name)
		}
		seen[name] = true
	}
	return nil
}

func applyExtensions(
	opts *client.Options,
	extensions []ClientConfigExtension,
	req ClientConfigExtensionRequest,
) error {
	for _, ext := range extensions {
		plugin, err := ext.ClientPlugin(req)
		if err != nil {
			return fmt.Errorf("client config extension %q: %w", ext.Name(), err)
		}
		if plugin != nil {
			opts.Plugins = append(opts.Plugins, plugin)
		}
	}
	return nil
}
