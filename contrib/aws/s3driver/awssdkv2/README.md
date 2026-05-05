# AWS SDK v2 Driver Client for S3 Storage Driver

> ⚠️ **This package is currently at an experimental release stage.** ⚠️

Package `go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2` wraps an [`*s3.Client`](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3#Client) from the AWS SDK for Go v2 to implement the [`s3driver.Client`](../README.md) interface. Import this package alongside [`s3driver`](../README.md) when using the official AWS SDK v2.

## Usage

```go
import (
    "context"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "go.temporal.io/sdk/contrib/aws/s3driver"
    "go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2"
)

cfg, err := config.LoadDefaultConfig(context.Background())
if err != nil {
    // handle error
}

driver, err := s3driver.NewDriver(s3driver.Options{
    Client: awssdkv2.NewClient(s3.NewFromConfig(cfg)),
    Bucket: s3driver.StaticBucket("my-temporal-payloads"),
})
```

See the [`s3driver` README](../README.md) for full documentation including dynamic bucket selection, key structure, notes, and required IAM permissions.
