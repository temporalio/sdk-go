# sdk-go CI jobs

The initial investigator monitors the `CI` workflow only. It excludes the
investigator itself and does not treat formatting, vulnerability, or changelog
policy failures as flakes.

The monitored workflow currently contains:

- `check`: repository checks on Ubuntu, macOS, and Windows.
- `unit-test`: unit tests on Ubuntu, Intel macOS, Apple Silicon macOS, and
  Windows using Go `oldstable` and `stable`.
- `integ-test`: integration tests with `WORKFLOW_CACHE_SIZE=0`.
- `integ-test-cache`: integration tests with the default workflow cache.
- `docker-compose-test`: integration tests against the samples-server Docker
  Compose environment.
- `cloud-test`: a small cloud-backed integration subset; external service,
  certificate, namespace, or availability failures require especially strong
  evidence before proposing an SDK change.
- `features-test`: a reusable cross-SDK feature workflow. Distinguish failures
  in the external reusable workflow or feature harness from sdk-go defects.

Matrix job display names include their platform and Go version. Preserve those
dimensions in normalized signatures. Windows, macOS Intel, macOS ARM, cache
disabled, Docker, cloud, and feature-harness failures are not interchangeable
inputs.

Known infrastructure-like evidence includes runner provisioning failures,
GitHub service failures, action download failures, unavailable external cloud
services, expired credentials, and container registry failures. Do not turn
these into SDK source changes unless repository evidence demonstrates an SDK
root cause.
