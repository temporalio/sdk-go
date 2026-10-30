// This file contains example code for a ClientConfigExtension.
// The code itself isn't actually used anywhere.
package envconfig_test

import (
	"context"
	"fmt"
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/temporal"
)

// identityExtension sets the client identity from config like:
//
//	[profile.default.ext.identity]
//	value = "my-identity"
//
// or from the TEMPORAL_EXT_IDENTITY_VALUE env var, which takes precedence.
type identityExtension struct{}

func (identityExtension) Name() string { return "identity" }

func (identityExtension) ClientPlugin(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
	var identity string
	if raw, ok := req.Profile.Ext["identity"]["value"]; ok {
		if identity, ok = raw.(string); !ok {
			return nil, fmt.Errorf("value must be a string, got %T", raw)
		}
	}
	if req.Env != nil {
		if s, ok := req.Env.LookupEnv("TEMPORAL_EXT_IDENTITY_VALUE"); ok {
			identity = s
		}
	}
	if identity == "" {
		return nil, nil
	}
	return temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "example.IdentityExtension",
		ConfigureClient: func(_ context.Context, options client.PluginConfigureClientOptions) error {
			options.ClientOptions.Identity = identity
			return nil
		},
	})
}

func ExampleClientConfigExtension() {
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		Extensions: []envconfig.ClientConfigExtension{identityExtension{}},
	})
	if err != nil {
		log.Fatalf("failed loading client options: %v", err)
	}
	c, err := client.Dial(opts)
	if err != nil {
		log.Fatalf("failed connecting client: %v", err)
	}
	defer c.Close()
}
