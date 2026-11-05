package envconfig_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
)

type testExtension struct {
	name         string
	clientPlugin func(envconfig.ClientConfigExtensionRequest) (client.Plugin, error)
}

func (e *testExtension) Name() string { return e.name }

func (e *testExtension) ClientPlugin(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
	return e.clientPlugin(req)
}

type testPlugin struct {
	client.PluginBase
	name string
}

func (p *testPlugin) Name() string { return p.name }

func TestLoadClientOptionsExtensions(t *testing.T) {
	env := EnvLookupMap{"TEMPORAL_ADDRESS": "env-address", "TEMPORAL_FIRST_VALUE": "env-value"}
	var calls []string
	first := &testExtension{name: "first", clientPlugin: func(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
		calls = append(calls, "first")
		// Env vars have already been applied to the profile
		require.Equal(t, "env-address", req.Profile.Address)
		require.Equal(t, map[string]any{"value": "file-value"}, req.Profile.Ext["first"])
		require.Equal(t, env, req.Env)
		v, _ := req.Env.LookupEnv("TEMPORAL_FIRST_VALUE")
		return &testPlugin{name: "first-" + v}, nil
	}}
	noop := &testExtension{name: "noop", clientPlugin: func(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
		calls = append(calls, "noop")
		require.Nil(t, req.Profile.Ext["noop"])
		return nil, nil
	}}
	second := &testExtension{name: "second", clientPlugin: func(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
		calls = append(calls, "second")
		return &testPlugin{name: "second"}, nil
	}}

	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`
		[profile.default]
		address = "file-address"
		[profile.default.ext.first]
		value = "file-value"`),
		ConfigFileStrict: true,
		EnvLookup:        env,
		Extensions:       []envconfig.ClientConfigExtension{second, first, noop},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first", "noop"}, calls) // extension order is respected
	require.Len(t, opts.Plugins, 2)
	require.Equal(t, "second", opts.Plugins[0].Name())
	require.Equal(t, "first-env-value", opts.Plugins[1].Name())
}

func TestLoadClientOptionsExtensionsEnv(t *testing.T) {
	var gotEnv envconfig.EnvLookup
	ext := &testExtension{name: "ext", clientPlugin: func(req envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
		gotEnv = req.Env
		return nil, nil
	}}

	// Defaults to OS env
	_, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`[profile.default]`),
		Extensions:     []envconfig.ClientConfigExtension{ext},
	})
	require.NoError(t, err)
	require.Equal(t, envconfig.EnvLookupOS, gotEnv)

	// Nil when env is disabled
	_, err = envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`[profile.default]`),
		DisableEnv:     true,
		EnvLookup:      EnvLookupMap{},
		Extensions:     []envconfig.ClientConfigExtension{ext},
	})
	require.NoError(t, err)
	require.Nil(t, gotEnv)
}

func TestLoadClientOptionsExtensionsInvalid(t *testing.T) {
	newExt := func(name string) *testExtension {
		return &testExtension{name: name, clientPlugin: func(envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
			t.Fatal("extension should not be called")
			return nil, nil
		}}
	}
	for name, tc := range map[string]struct {
		extensions  []envconfig.ClientConfigExtension
		expectedErr string
	}{
		"duplicate": {
			extensions:  []envconfig.ClientConfigExtension{newExt("a"), newExt("b"), newExt("a")},
			expectedErr: `duplicate client config extension name "a"`,
		},
		"empty name": {
			extensions:  []envconfig.ClientConfigExtension{newExt("a"), newExt("")},
			expectedErr: "client config extension at index 1 (*envconfig_test.testExtension) has empty name",
		},
		"nil": {
			extensions:  []envconfig.ClientConfigExtension{nil},
			expectedErr: "client config extension at index 0 is nil",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
				ConfigFileData: []byte(`[profile.default]`),
				EnvLookup:      EnvLookupMap{},
				Extensions:     tc.extensions,
			})
			require.EqualError(t, err, tc.expectedErr)
		})
	}
}

func TestLoadClientOptionsExtensionError(t *testing.T) {
	extErr := errors.New("bad config")
	_, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{
		ConfigFileData: []byte(`[profile.default]`),
		EnvLookup:      EnvLookupMap{},
		Extensions: []envconfig.ClientConfigExtension{
			&testExtension{name: "bad", clientPlugin: func(envconfig.ClientConfigExtensionRequest) (client.Plugin, error) {
				return nil, extErr
			}},
		},
	})
	require.ErrorIs(t, err, extErr)
	require.EqualError(t, err, `client config extension "bad": bad config`)
}
