# Cloud Storage Client for GCS Storage Driver

> ⚠️ **This package is currently at an experimental release stage.** ⚠️

Package `go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk` wraps a [`*storage.Client`](https://pkg.go.dev/cloud.google.com/go/storage#Client) from the Cloud Storage Go client library to implement the [`gcsdriver.Client`](../README.md) interface. Import this package alongside [`gcsdriver`](../README.md) when using the official Google Cloud Storage SDK.

## Usage

```go
import (
    "context"

    "cloud.google.com/go/storage"
    "go.temporal.io/sdk/contrib/google/gcsdriver"
    "go.temporal.io/sdk/contrib/google/gcsdriver/gcssdk"
)

gcsClient, err := storage.NewClient(context.Background())
if err != nil {
    // handle error
}

driver, err := gcsdriver.NewDriver(gcsdriver.Options{
    Client: gcssdk.NewClient(gcsClient),
    Bucket: gcsdriver.StaticBucket("my-temporal-payloads"),
})
```

See the [`gcsdriver` README](../README.md) for full documentation including dynamic bucket selection, key structure, notes, and required IAM permissions.
