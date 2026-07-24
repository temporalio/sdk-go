# GCS Storage Driver for Temporal Go SDK

> ⚠️ **This package is currently at an experimental release stage.** ⚠️

Package `go.temporal.io/sdk/contrib/google/gcsdriver` provides a GCS-backed [`converter.StorageDriver`](https://pkg.go.dev/go.temporal.io/sdk/converter#StorageDriver) for the Temporal Go SDK's [external storage](https://pkg.go.dev/go.temporal.io/sdk/converter#ExternalStorage) system. Large payloads are offloaded to Google Cloud Storage and replaced with a storage reference in the Temporal history event; the reference is resolved back to the original payload before it reaches application code.

## Usage

The `go.temporal.io/sdk/contrib/google/gcsdriver` package defines the driver and its configuration. Use the companion package `go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk` to wrap a Cloud Storage client.

```go
import (
    "context"

    "cloud.google.com/go/storage"
    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/contrib/google/gcsdriver"
    "go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk"
    "go.temporal.io/sdk/converter"
)

gcsClient, err := storage.NewClient(context.Background())
if err != nil {
    // handle error
}

driver, err := gcsdriver.NewDriver(gcsdriver.Options{
    Client: gcssdk.NewClient(gcsClient),
    Bucket: gcsdriver.StaticBucket("my-temporal-payloads"),
})
if err != nil {
    // handle error
}

c, err := client.Dial(client.Options{
    HostPort:  "localhost:7233",
    ExternalStorage: converter.ExternalStorage{
        Drivers: []converter.StorageDriver{driver},
    },
})
```

Credentials are resolved automatically from Application Default Credentials (ADC) — environment variables, `gcloud auth application-default login`, workload identity, and so on.

## Key Structure

Payloads are stored under content-addressable keys derived from a SHA-256 hash of the serialized payload bytes, segmented by Namespace and Workflow/Standalone Activity identifiers when the target is available:

```
# Workflow payload
v0/ns/<namespace>/wt/<workflow-type>/wi/<workflow-id>/ri/<run-id>/d/sha256/<hash>

# Standalone Activity payload
v0/ns/<namespace>/at/<activity-type>/ai/<activity-id>/ri/<run-id>/d/sha256/<hash>

# Unknown context (fallback)
v0/d/sha256/<hash>
```

Special characters in path segments are percent-encoded. Empty segments are replaced with `null`.

## Notes

- Any driver used to store payloads must also be configured on the component that retrieves them. If the client stores Workflow inputs using this driver, the worker must include it in its `ExternalStorage.Drivers` list to retrieve them.
- The target GCS bucket must already exist; the driver will not create it.
- Identical serialized bytes within the same Namespace and Workflow (or Standalone Activity) share the same GCS object — the key is content-addressable within that scope. The same bytes used across different Workflows or Namespaces produce distinct GCS objects because the key includes the Namespace and Workflow/Standalone Activity identifiers.
- Only payloads at or above `ExternalStorage.PayloadSizeThreshold` (default: 256 KiB) are offloaded; smaller payloads are stored inline. Set `ExternalStorage.PayloadSizeThreshold` to `0` or leave unset to use the default threshold. To store all payloads in external storage, set `ExternalStorage.PayloadSizeThreshold` to `1`.
- `Options.MaxPayloadSize` (default: 50 MiB) sets a hard upper limit on the serialized size of any single payload. An error is returned at store time if a payload exceeds this limit.
- Override `Options.DriverName` only when registering multiple `gcsdriver` instances with distinct configurations under the same `ExternalStorage.Drivers` list.

## Dynamic Bucket Selection

To select the GCS bucket per payload, pass a `BucketFunc` as `Options.Bucket` instead of using `StaticBucket`:

```go
driver, err := gcsdriver.NewDriver(gcsdriver.Options{
    Client: gcssdk.NewClient(gcsClient),
    Bucket: func(ctx converter.StorageDriverStoreContext, payload *commonpb.Payload) string {
        if len(payload.GetData()) > 10*1024*1024 {
            return "large-payloads"
        }
        return "small-payloads"
    },
})
```

The above example stores payloads in the `large-payloads` bucket if their size is greater than 10 MiB; otherwise, the payloads are stored in the `small-payloads` bucket.

## Required IAM Permissions

The Google Cloud credentials used by your Storage client must have the following IAM permissions on the target bucket:

- `storage.objects.create` — required by components that store payloads (typically the Temporal Client and Workers sending Workflow/Activity inputs and results).
- `storage.objects.get` — required by components that retrieve payloads (typically Workers and Clients reading inputs and results).

The predefined `roles/storage.objectUser` role includes both permissions. Components that only retrieve payloads can use the narrower `roles/storage.objectViewer` role.

## Custom GCS Driver Client Implementations

To use a GCS-compatible storage service or a different client library, implement the `Client` interface directly. It has no dependency on any Google Cloud package:

```go
type Client interface {
    PutObject(ctx context.Context, bucket, key string, data []byte) error
    ObjectExists(ctx context.Context, bucket, key string) (bool, error)
    GetObject(ctx context.Context, bucket, key string) ([]byte, error)
}
```

Pass your implementation as `Options.Client` when calling `NewDriver`.

## ObjectExists Behavior

The GCS driver's `ObjectExists` implementation returns `(false, error)` when the parent bucket does not exist. This differs from the S3 driver, which returns `(false, nil)` in the same scenario due to how the AWS SDK maps `HeadObject` responses. The GCS behavior is a deliberate choice based on the `Client` interface contract: *"a non-nil error only when the existence of the object cannot be determined."* A missing bucket means existence truly cannot be determined, so an error is the more correct response. Bucket existence is cached after the first successful check, so this adds at most one extra RPC per unique bucket.
