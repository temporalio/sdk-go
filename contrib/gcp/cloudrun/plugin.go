package cloudrun

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// pluginName is the name reported by [Plugin.Name].
const pluginName = "temporal-cloudrun-worker-id"

// PluginOptions configures [NewPlugin]. The zero value is valid and is the normal choice on Cloud
// Run: the plugin then reads the deployment name and revision from the environment and fetches the
// instance ID from the GCP metadata server. The remaining fields are dependency-injection knobs for
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

// Plugin configures a Temporal client and its workers from Google Cloud Run instance metadata,
// covering both Cloud Run worker pools and Cloud Run services. It implements both
// [go.temporal.io/sdk/client.Plugin] and [go.temporal.io/sdk/worker.Plugin]: register it once on
// [go.temporal.io/sdk/client.Options.Plugins] and it automatically propagates to every worker
// created from the client.
//
// When the client connects, the plugin fetches the Cloud Run metadata once (see [FetchMetadata]),
// caches it, and sets the client [go.temporal.io/sdk/client.Options.Identity] to the derived worker
// identity unless the caller already set one — a user-provided identity always wins. For each worker
// it sets [go.temporal.io/sdk/worker.Options.DeploymentOptions] to opt into Worker Deployment
// Versioning with the Cloud Run deployment version, pinning workflows to this version by default
// ([go.temporal.io/sdk/workflow.VersioningBehaviorPinned]; a per-workflow versioning behavior takes
// precedence).
//
// If the metadata fetch fails — typically because the process is not running on a Cloud Run worker
// pool or service — client creation fails with a clear error rather than silently doing nothing. Set
// [PluginOptions.Metadata] to inject metadata and avoid the fetch in tests or advanced use.
//
// A single Plugin may be registered on multiple clients; the metadata is fetched once and shared.
//
// Experimental: Google Cloud Run support is experimental and its API may change in a future release.
type Plugin struct {
	pluginClientBase
	pluginWorkerBase

	metadataURL string
	httpClient  *http.Client

	mu       sync.Mutex
	metadata *Metadata
}

// pluginClientBase and pluginWorkerBase let [Plugin] embed both SDK plugin bases at once. Embedding
// client.PluginBase and worker.PluginBase directly is not possible because both fields would be
// named PluginBase; wrapping each in a distinct named type avoids the collision.
type pluginClientBase struct{ client.PluginBase }
type pluginWorkerBase struct{ worker.PluginBase }

var (
	_ client.Plugin = (*Plugin)(nil)
	_ worker.Plugin = (*Plugin)(nil)
)

// NewPlugin creates a [Plugin] that reads Google Cloud Run instance metadata and applies the derived
// worker identity and Worker Deployment Version to a Temporal client and its workers. See [Plugin]
// for the behavior and [PluginOptions] for the dependency-injection knobs.
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
// resolved identity and deployment version.
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

// ConfigureWorker opts the worker into Worker Deployment Versioning using the Cloud Run deployment
// version, pinning workflows to this version by default ([workflow.VersioningBehaviorPinned]).
//
// It never returns an error: worker.New turns a ConfigureWorker error into a panic, and the metadata
// was already fetched — and any failure surfaced — in ConfigureClient. In the unexpected case that
// the metadata is unavailable or incomplete (for example the deployment name or revision is empty),
// the worker options are left unchanged rather than panicking.
func (p *Plugin) ConfigureWorker(_ context.Context, options worker.PluginConfigureWorkerOptions) error {
	if options.WorkerOptions == nil {
		return nil
	}
	md := p.Metadata()
	if md == nil {
		return nil
	}
	version, err := md.DeploymentVersion()
	if err != nil {
		return nil
	}
	options.WorkerOptions.DeploymentOptions = worker.DeploymentOptions{
		UseVersioning:             true,
		Version:                   version,
		DefaultVersioningBehavior: workflow.VersioningBehaviorPinned,
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
