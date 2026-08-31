<!--
Release notes for go.temporal.io/sdk/contrib/gcp/cloudrun.
Loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Add user-facing changes below under the appropriate heading (create the heading
if it does not yet exist): Added, Changed, Deprecated, Breaking Changes, Fixed,
or Security.
-->

# Changelog

## [Unreleased]

### Added

- Initial (experimental) release of the Cloud Run integration. `Plugin` is a
  client-and-worker plugin: register it once on `client.Options.Plugins` and, when the
  client connects, it reads the current Cloud Run instance's metadata and sets the
  client identity (unless one is already set) and each worker's Worker Deployment
  Version, pinning workflows to that version by default. It supports both Cloud Run
  worker pools (`CLOUD_RUN_WORKER_POOL`, `CLOUD_RUN_REVISION`) and Cloud Run services
  (`K_SERVICE`, `K_REVISION`), reading the unique instance ID from the GCP metadata
  server, and fails fast with a clear error when the process is not running on Cloud
  Run. The lower-level `FetchMetadata` reader and the `Metadata` type (with the
  `WorkerIdentity` and `DeploymentVersion` accessors) remain available for advanced use
  and for dependency injection into the plugin via `PluginOptions`. Unlike
  `contrib/aws/lambdaworker`, this is a plugin rather than a worker wrapper, because
  Cloud Run runs a long-lived container.
