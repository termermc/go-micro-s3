package client

import (
	"context"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// Buckets lists all buckets owned by the authenticated user.
func (c *Client) Buckets(ctx context.Context) ([]api.BucketInfo, error) {
	out, err := c.api.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}

	return out.Buckets, nil
}

// CreateBucket creates a bucket.
func (c *Client) CreateBucket(ctx context.Context, bucket string) error {
	return c.api.CreateBucket(ctx, &api.CreateBucketInput{
		Bucket: bucket,
	})
}

// DeleteBucket deletes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, bucket string) error {
	return c.api.DeleteBucket(ctx, &api.DeleteBucketInput{
		Bucket: bucket,
	})
}

// BucketExists reports whether a bucket exists and is accessible.
func (c *Client) BucketExists(ctx context.Context, bucket string) (bool, error) {
	err := c.api.HeadBucket(ctx, &api.HeadBucketInput{
		Bucket: bucket,
	})
	if err != nil {
		if api.IsNotFound(api.ErrNotFound) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}
