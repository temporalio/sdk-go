## [Unreleased]

### Added

- Initial (experimental) release of the Cloud Run integration. `Plugin` is a client
  plugin: register it once on `client.Options.Plugins` and, when the client connects, it
  reads the current Cloud Run instance's metadata and sets the client identity (unless one
  is already set), which every worker created from the client inherits. It supports both
  Cloud Run worker pools (`CLOUD_RUN_WORKER_POOL`, `CLOUD_RUN_REVISION`) and Cloud Run
  services (`K_SERVICE`, `K_REVISION`), reading the unique instance ID from the GCP
  metadata server, and returns an error when the process is not running on
  Cloud Run. The lower-level `FetchMetadata` reader and the `Metadata` type (with the
  `WorkerIdentity` accessor) remain available for advanced use and for dependency
  injection into the plugin via `PluginOptions`.
