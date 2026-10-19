package api

import (
	"context"
	"encoding/xml"
	"net/http"
)

// ListBucketsOutput is the result of ListBuckets.
type ListBucketsOutput struct {
	Owner   Owner
	Buckets []BucketInfo
}

// ListBuckets lists all buckets owned by the authenticated user.
func (c *Client) ListBuckets(ctx context.Context) (*ListBucketsOutput, error) {
	resp, err := c.do(ctx, &request{method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	var res listAllMyBucketsResult
	if err := decodeXML(resp, &res); err != nil {
		return nil, err
	}
	return &ListBucketsOutput{Owner: res.Owner, Buckets: res.Buckets}, nil
}

// CreateBucketInput is the input to CreateBucket.
type CreateBucketInput struct {
	Bucket string
}

// CreateBucket creates a bucket. A LocationConstraint matching the client's
// region is sent unless the region is us-east-1.
func (c *Client) CreateBucket(ctx context.Context, in *CreateBucketInput) error {
	r := &request{method: http.MethodPut, bucket: in.Bucket}
	if c.region != "us-east-1" {
		body, err := xml.Marshal(createBucketConfiguration{XMLNS: s3XMLNS, LocationConstraint: c.region})
		if err != nil {
			return err
		}
		r.payload = body
		r.header = http.Header{"Content-Type": {"application/xml"}}
	}
	_, err := c.doClose(ctx, r)
	return err
}

// DeleteBucketInput is the input to DeleteBucket.
type DeleteBucketInput struct {
	Bucket string
}

// DeleteBucket deletes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, in *DeleteBucketInput) error {
	_, err := c.doClose(ctx, &request{method: http.MethodDelete, bucket: in.Bucket})
	return err
}

// HeadBucketInput is the input to HeadBucket.
type HeadBucketInput struct {
	Bucket string
}

// HeadBucket checks that a bucket exists and is accessible. A missing bucket
// yields an error matching ErrNotFound.
func (c *Client) HeadBucket(ctx context.Context, in *HeadBucketInput) error {
	_, err := c.doClose(ctx, &request{method: http.MethodHead, bucket: in.Bucket})
	return err
}
