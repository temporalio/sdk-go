# S3 Storage Driver for Temporal Go SDK

> ⚠️ **This package is currently at an experimental release stage.** ⚠️

Package `go.temporal.io/sdk/contrib/aws/s3driver` provides an S3-backed [`converter.StorageDriver`](https://pkg.go.dev/go.temporal.io/sdk/converter#StorageDriver) for the Temporal Go SDK's [external storage](https://pkg.go.dev/go.temporal.io/sdk/converter#ExternalStorage) system. Large payloads are offloaded to Amazon S3 and replaced with a storage reference in the Temporal history event; the reference is resolved back to the original payload before it reaches application code.

## Usage

The `go.temporal.io/sdk/contrib/aws/s3driver` package defines the driver and its configuration. Use the companion package `go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2` to wrap an AWS SDK v2 S3 client.

```go
import (
    "context"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/contrib/aws/s3driver"
    "go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2"
    "go.temporal.io/sdk/converter"
)

cfg, err := config.LoadDefaultConfig(context.Background())
if err != nil {
    // handle error
}

driver, err := s3driver.NewDriver(s3driver.Options{
    Client: awssdkv2.NewClient(s3.NewFromConfig(cfg)),
    Bucket: s3driver.StaticBucket("my-temporal-payloads"),
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

Credentials and region are resolved automatically from the standard AWS credential chain (environment variables, `~/.aws/config`, IAM instance profile, and so on).

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
- The target S3 bucket must already exist; the driver will not create it.
- Identical serialized bytes within the same Namespace and Workflow (or Standalone Activity) share the same S3 object — the key is content-addressable within that scope. The same bytes used across different Workflows or Namespaces produce distinct S3 objects because the key includes the Namespace and Workflow/Standalone Activity identifiers.
- Only payloads at or above `ExternalStorage.PayloadSizeThreshold` (default: 256 KiB) are offloaded; smaller payloads are stored inline. Set `ExternalStorage.PayloadSizeThreshold` to `0` or leave unset to use the default threshold. To store all payloads in external storage, set `ExternalStorage.PayloadSizeThreshold` to `1`.
- `Options.MaxPayloadSize` (default: 50 MiB) sets a hard upper limit on the serialized size of any single payload. An error is returned at store time if a payload exceeds this limit.
- Override `Options.DriverName` only when registering multiple `s3driver` instances with distinct configurations under the same `ExternalStorage.Drivers` list.

## Dynamic Bucket Selection

To select the S3 bucket per payload, pass a `BucketFunc` as `Options.Bucket` instead of using `StaticBucket`:

```go
driver, err := s3driver.NewDriver(s3driver.Options{
    Client: awssdkv2.NewClient(s3Client),
    Bucket: func(ctx converter.StorageDriverStoreContext, payload *commonpb.Payload) string {
        if len(payload.GetData()) > 10*1024*1024 {
            return "large-payloads"
        }
        return "small-payloads"
    },
})
```

The above example stores payloads in the `large-payloads` bucket if their size is greater thatn 10 MiB; otherwise, the payloads are stored in the "small-payloads" bucket.

## Required IAM Permissions

The AWS credentials used by your S3 client must have the following S3 permissions on the target bucket and its objects:

```json
{
  "Effect": "Allow",
  "Action": [
    "s3:PutObject",
    "s3:GetObject"
  ],
  "Resource": "arn:aws:s3:::my-temporal-payloads/*"
}
```

`s3:PutObject` is required by components that store payloads (typically the Temporal client and workers sending Workflow/Activity inputs and results), and `s3:GetObject` is required by components that retrieve them (typically Workers and Clients reading results). Components that only retrieve payloads do not need `s3:PutObject`, and vice versa.

## Custom S3 Driver Client Implementations

To use a different AWS SDK version or an S3-compatible storage service, implement the `Client` interface directly. It has no dependency on any AWS package:

```go
type Client interface {
    PutObject(ctx context.Context, bucket, key string, data []byte) error
    ObjectExists(ctx context.Context, bucket, key string) (bool, error)
    GetObject(ctx context.Context, bucket, key string) ([]byte, error)
}
```

Pass your implementation as `Options.Client` when calling `NewDriver`.
