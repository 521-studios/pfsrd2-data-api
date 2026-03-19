// Package s3 provides S3 helpers for fetching full JSON objects and DB metadata.
package s3

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectFetcher is the subset of Client used by handlers to fetch JSON objects.
// The real *Client satisfies this; tests can provide a mock.
type ObjectFetcher interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
	GetObjectBytes(ctx context.Context, key string) ([]byte, error)
}

// Client wraps the AWS S3 client with bucket-scoped helpers.
type Client struct {
	inner  *awss3.Client
	bucket string
}

// New creates an S3 client from the provided AWS config.
func New(cfg aws.Config, bucket string) *Client {
	return &Client{
		inner:  awss3.NewFromConfig(cfg),
		bucket: bucket,
	}
}

// GetObject fetches the full body of an S3 object by key.
// Caller is responsible for closing the returned ReadCloser.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.inner.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 GetObject %s: %w", key, err)
	}
	return out.Body, nil
}

// GetObjectBytes fetches the full body of an S3 object and returns it as bytes.
func (c *Client) GetObjectBytes(ctx context.Context, key string) ([]byte, error) {
	body, err := c.GetObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

// HeadObject returns the ETag (without quotes) of an S3 object, or "" if not found.
func (c *Client) HeadObject(ctx context.Context, key string) (etag string, err error) {
	out, err := c.inner.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("s3 HeadObject %s: %w", key, err)
	}
	if out.ETag == nil {
		return "", nil
	}
	etag = *out.ETag
	// Strip surrounding quotes from S3 ETag
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		etag = etag[1 : len(etag)-1]
	}
	return etag, nil
}

// DBKey is the S3 key for the SQLite database.
const DBKey = "db/pfsrd2.db"
