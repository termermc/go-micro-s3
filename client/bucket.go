package client

import (
	"context"
	"net/http"

	"github.com/termermc/go-micro-s3/client/api"
)

// BucketsOptions are optional parameters for Client.Buckets.
type BucketsOptions struct {
	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// Buckets lists all buckets owned by the authenticated user.
// opts may be nil.
func (c *Client) Buckets(ctx context.Context, opts *BucketsOptions) ([]api.BucketInfo, error) {
	if opts == nil {
		opts = &BucketsOptions{}
	}

	out, err := c.api.ListBuckets(ctx, &api.ListBucketsInput{
		ExtraHeaders: opts.ExtraHeaders,
	})
	if err != nil {
		return nil, err
	}

	return out.Buckets, nil
}

// CreateBucketOptions are optional parameters for Client.CreateBucket.
type CreateBucketOptions struct {
	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// CreateBucket creates a bucket.
// opts may be nil.
func (c *Client) CreateBucket(ctx context.Context, bucket string, opts *CreateBucketOptions) error {
	if opts == nil {
		opts = &CreateBucketOptions{}
	}

	return c.api.CreateBucket(ctx, &api.CreateBucketInput{
		Bucket:       bucket,
		ExtraHeaders: opts.ExtraHeaders,
	})
}

// DeleteBucketOptions are optional parameters for Client.DeleteBucket.
type DeleteBucketOptions struct {
	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// DeleteBucket deletes an empty bucket.
// opts may be nil.
func (c *Client) DeleteBucket(ctx context.Context, bucket string, opts *DeleteBucketOptions) error {
	if opts == nil {
		opts = &DeleteBucketOptions{}
	}

	return c.api.DeleteBucket(ctx, &api.DeleteBucketInput{
		Bucket:       bucket,
		ExtraHeaders: opts.ExtraHeaders,
	})
}

// BucketExistsOptions are optional parameters for Client.BucketExists.
type BucketExistsOptions struct {
	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// BucketExists reports whether a bucket exists and is accessible.
// opts may be nil.
func (c *Client) BucketExists(ctx context.Context, bucket string, opts *BucketExistsOptions) (bool, error) {
	if opts == nil {
		opts = &BucketExistsOptions{}
	}

	err := c.api.HeadBucket(ctx, &api.HeadBucketInput{
		Bucket:       bucket,
		ExtraHeaders: opts.ExtraHeaders,
	})
	if err != nil {
		if api.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}
