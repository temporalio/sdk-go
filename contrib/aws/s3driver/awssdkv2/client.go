// Package awssdkv2 provides an s3driver.Client implementation backed by the
// AWS SDK for Go v2. Import this package alongside s3driver when using the
// official AWS SDK v2 client; use a different adapter package for other
// SDK versions.
//
// NOTE: Experimental
package awssdkv2

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"go.temporal.io/sdk/contrib/aws/s3driver"
)

type s3Client struct {
	client *s3.Client
}

// NewClient creates an s3driver.Client backed by an AWS SDK v2 S3 client.
//
// NOTE: Experimental
func NewClient(client *s3.Client) s3driver.Client {
	return &s3Client{client: client}
}

func (c *s3Client) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   bytes.NewReader(data),
	})
	return err
}

func (c *s3Client) ObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		// HeadObject returns a smithy APIError with code "NotFound" for
		// missing objects; some SDK versions also surface *types.NotFound.
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NotFound" {
			return false, nil
		}
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *s3Client) Describe() map[string]string {
	region := c.client.Options().Region
	if region == "" {
		return nil
	}
	return map[string]string{"client_region": region}
}

func (c *s3Client) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	output, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer output.Body.Close()
	return io.ReadAll(output.Body)
}
