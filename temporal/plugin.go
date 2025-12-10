package temporal

import "go.temporal.io/sdk/internal"

// SimplePlugin implements both [go.temporal.io/sdk/client.Plugin] and
// [go.temporal.io/sdk/worker.Plugin] from a given set of options. Use
// [go.temporal.io/sdk/temporal.NewSimplePlugin] to instantiate this.
type SimplePlugin = internal.SimplePlugin

// SimplePluginOptions are options for NewSimplePlugin.
type SimplePluginOptions = internal.SimplePluginOptions

// SimplePluginRunContextBeforeOptions are options for RunContextBefore on a
// simple plugin.
type SimplePluginRunContextBeforeOptions = internal.SimplePluginRunContextBeforeOptions

// SimplePluginRunContextAfterOptions are options for RunContextAfter on a
// simple plugin.
type SimplePluginRunContextAfterOptions = internal.SimplePluginRunContextAfterOptions

// NewSimplePlugin creates a new SimplePlugin with the given options.
func NewSimplePlugin(options SimplePluginOptions) (*SimplePlugin, error) {
	return internal.NewSimplePlugin(options)
}
