package workerid

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"go.temporal.io/sdk/client"
)

// pluginName is the name reported by [Plugin.Name].
const pluginName = "temporal-cloudrun-worker-id"

// PluginOptions configures [NewPlugin]. The zero value is valid and is the normal choice on Cloud
// Run: the plugin then reads the worker pool (or service) name and revision from the environment
// and fetches the instance ID from the GCP metadata server. The remaining fields are dependency-injection knobs for
// tests and advanced use.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
type PluginOptions struct {
	// Metadata, if non-nil, supplies the Cloud Run metadata directly instead of reading the
	// environment and querying the metadata server. When set, the plugin performs no network
	// request and never fails at connect time. It is primarily useful for tests and for advanced
	// callers that fetch the metadata themselves with [FetchMetadata].
	Metadata *Metadata

	// MetadataURL overrides the metadata server URL used to fetch the instance ID. It is primarily
	// useful for testing. Ignored when Metadata is set. See [WithMetadataURL].
	MetadataURL string

	// HTTPClient overrides the [http.Client] used to query the metadata server, to set a custom
	// timeout or transport, or for testing. Ignored when Metadata is set. See [WithHTTPClient].
	HTTPClient *http.Client
}

// Plugin sets a Temporal client's identity from Google Cloud Run instance metadata, covering both
// Cloud Run worker pools and Cloud Run services. It implements [go.temporal.io/sdk/client.Plugin]:
// register it once on [go.temporal.io/sdk/client.Options.Plugins] and every worker created from the
// client inherits the identity.
//
// When the client connects, the plugin fetches the Cloud Run metadata once (see [FetchMetadata]),
// caches it, and sets the client [go.temporal.io/sdk/client.Options.Identity] to the derived worker
// identity unless the caller already set one — a user-provided identity always wins.
//
// If the metadata fetch fails — typically because the process is not running on a Cloud Run worker
// pool or service — client creation fails with a clear error rather than silently doing nothing. Set
// [PluginOptions.Metadata] to inject metadata and avoid the fetch in tests or advanced use.
//
// A single Plugin may be registered on multiple clients; the metadata is fetched once and shared.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
type Plugin struct {
	client.PluginBase

	metadataURL string
	httpClient  *http.Client

	mu       sync.Mutex
	metadata *Metadata
}

var _ client.Plugin = (*Plugin)(nil)

// NewPlugin creates a [Plugin] that reads Google Cloud Run instance metadata and applies the derived
// worker identity to a Temporal client. See [Plugin] for the behavior and [PluginOptions] for the
// dependency-injection knobs.
//
// The metadata is fetched lazily when the client connects, using the client's dial context — not
// here — so construction never performs a network request or returns an error.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
func NewPlugin(options PluginOptions) *Plugin {
	return &Plugin{
		metadataURL: options.MetadataURL,
		httpClient:  options.HTTPClient,
		metadata:    options.Metadata,
	}
}

// Name returns the plugin name.
func (*Plugin) Name() string { return pluginName }

// Metadata returns the Cloud Run instance metadata the plugin resolved, or nil if it has not been
// fetched yet (that is, before the client connects, unless it was injected via
// [PluginOptions.Metadata]). It is safe to call after connecting the client, for example to log the
// resolved identity.
func (p *Plugin) Metadata() *Metadata {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.metadata
}

// ConfigureClient fetches the Cloud Run instance metadata (once, caching it) and sets the derived
// worker identity on the client options unless the caller already set one. It returns an error when
// the metadata cannot be fetched, which typically means the process is not running on a Cloud Run
// worker pool or service.
func (p *Plugin) ConfigureClient(ctx context.Context, options client.PluginConfigureClientOptions) error {
	if options.ClientOptions == nil {
		return fmt.Errorf("cloudrun: client options are required")
	}
	md, err := p.ensureMetadata(ctx)
	if err != nil {
		return err
	}
	if options.ClientOptions.Identity == "" {
		options.ClientOptions.Identity = md.WorkerIdentity()
	}
	return nil
}

// ensureMetadata returns the injected or previously fetched metadata, fetching and caching it on
// first use. The fetch uses the provided context, which at connect time is the client's dial
// context. The lock is not held across the fetch; a rare concurrent first fetch is harmless.
func (p *Plugin) ensureMetadata(ctx context.Context) (*Metadata, error) {
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

// fetchOptions translates the plugin's dependency-injection knobs into [FetchMetadata] options.
func (p *Plugin) fetchOptions() []Option {
	var opts []Option
	if p.httpClient != nil {
		opts = append(opts, WithHTTPClient(p.httpClient))
	}
	if p.metadataURL != "" {
		opts = append(opts, WithMetadataURL(p.metadataURL))
	}
	return opts
}
