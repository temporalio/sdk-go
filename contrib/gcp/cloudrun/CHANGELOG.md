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

- Initial (experimental) release of the Cloud Run metadata helper. `FetchMetadata`
  reads the deployment name, revision, and unique instance ID of the current Cloud
  Run instance, supporting both Cloud Run worker pools (`CLOUD_RUN_WORKER_POOL`,
  `CLOUD_RUN_REVISION`) and Cloud Run services (`K_SERVICE`, `K_REVISION`), with the
  instance ID read from the GCP metadata server. The resulting `Metadata` applies a
  worker identity and a Worker Deployment Version to a normal, long-lived worker via
  `ApplyToClientOptions` and `ApplyToWorkerOptions` (or the `WorkerIdentity` and
  `DeploymentVersion` accessors). Unlike `contrib/aws/lambdaworker`, this is a
  metadata helper rather than a worker wrapper, because Cloud Run runs a long-lived
  container.
