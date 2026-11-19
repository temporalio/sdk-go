# System information provider for the Temporal Go SDK

Package `go.temporal.io/sdk/contrib/sysinfo` provides CPU and memory usage from
gopsutil, including cgroup-aware metrics on Linux. It implements
`worker.SysInfoProvider` for resource-based worker tuning and worker
heartbeats.

## Add to your project

From your application's Go module, run:

```bash
go get go.temporal.io/sdk/contrib/sysinfo@latest
```

## Module versioning

`sysinfo` is released as a separate Go module from the core Temporal Go SDK.
See [CHANGELOG.md](CHANGELOG.md) for release notes.

## Usage

Use the shared provider with a resource-based worker tuner:

```go
provider := sysinfo.SysInfoProvider()
tuner, err := worker.NewResourceBasedTuner(worker.ResourceBasedTunerOptions{
	TargetMem:    0.8,
	TargetCpu:    0.9,
	InfoSupplier: provider,
})
if err != nil {
	return err
}

w := worker.New(c, taskQueue, worker.Options{Tuner: tuner})
```

The same provider can be assigned to `worker.Options.SysInfoProvider` when CPU
and memory usage is needed for worker heartbeats without a resource-based
tuner. Reuse the provider instead of creating separate instances.
