package id

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"go.temporal.io/sdk/client"
)

// pluginName is the name reported by [CloudRunIDPlugin.Name].
const pluginName = "temporal-cloudrun-id"

// CloudRunIDPlugin sets a Temporal client's identity from Google Cloud Run instance metadata. It
// implements [go.temporal.io/sdk/client.Plugin]: register it once on
// [go.temporal.io/sdk/client.Options.Plugins] and every worker created from the client inherits the
// identity.
//
// When the client connects, the plugin fetches the metadata once (see [FetchMetadata]), caches it,
// and sets the client [go.temporal.io/sdk/client.Options.Identity] unless the caller already set one.
// If the fetch fails (usually because the process is not running on Cloud Run), client creation
// returns an error.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
type CloudRunIDPlugin struct {
	client.PluginBase

	metadataURL string
	httpClient  *http.Client

	mu       sync.Mutex
	metadata *Metadata
}

var _ client.Plugin = (*CloudRunIDPlugin)(nil)

// NewCloudRunIDPlugin creates a [CloudRunIDPlugin]. See [CloudRunIDPlugin] for the behavior. The
// metadata is fetched lazily when the client connects, so construction makes no network request.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
func NewCloudRunIDPlugin() *CloudRunIDPlugin {
	return &CloudRunIDPlugin{}
}

// Name returns the plugin name.
func (*CloudRunIDPlugin) Name() string { return pluginName }

// Metadata returns the resolved Cloud Run instance metadata, or nil if it has not been fetched yet
// (that is, before the client connects).
func (p *CloudRunIDPlugin) Metadata() *Metadata {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.metadata
}

// ConfigureClient fetches the Cloud Run metadata (once) and sets the derived client identity on the
// client options unless the caller already set one. It returns an error if the fetch fails.
func (p *CloudRunIDPlugin) ConfigureClient(ctx context.Context, options client.PluginConfigureClientOptions) error {
	if options.ClientOptions == nil {
		return fmt.Errorf("cloudrun: client options are required")
	}
	md, err := p.ensureMetadata(ctx)
	if err != nil {
		return err
	}
	if options.ClientOptions.Identity == "" {
		options.ClientOptions.Identity = md.Identity()
	}
	return nil
}

// ensureMetadata returns the injected or cached metadata, fetching and caching it on first use. The
// lock is not held across the fetch; a rare concurrent first fetch is harmless.
func (p *CloudRunIDPlugin) ensureMetadata(ctx context.Context) (*Metadata, error) {
	p.mu.Lock()
	md := p.metadata
	p.mu.Unlock()
	if md != nil {
		return md, nil
	}

	fetched, err := FetchMetadata(ctx, p.fetchOptions()...)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.metadata == nil {
		p.metadata = fetched
	}
	md = p.metadata
	p.mu.Unlock()
	return md, nil
}

// fetchOptions builds the [FetchMetadata] options from the plugin's fields.
func (p *CloudRunIDPlugin) fetchOptions() []Option {
	var opts []Option
	if p.httpClient != nil {
		opts = append(opts, WithHTTPClient(p.httpClient))
	}
	if p.metadataURL != "" {
		opts = append(opts, WithMetadataURL(p.metadataURL))
	}
	return opts
}
