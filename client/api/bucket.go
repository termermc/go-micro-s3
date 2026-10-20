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
	resp, err := c.Do(ctx, &Request{
		Method: http.MethodGet,
	})
	if err != nil {
		return nil, err
	}

	var res listAllMyBucketsResult
	if err = decodeXmlBody(resp, &res, false); err != nil {
		return nil, err
	}

	return &ListBucketsOutput{Owner: res.Owner, Buckets: res.Buckets}, nil
}

// CreateBucketInput is the input to Client.CreateBucket.
type CreateBucketInput struct {
	// The bucket's name.
	Bucket string

	// If true, the bucket's location will not be constrained to the client's region.
	NoLocationConstraint bool
}

// CreateBucket creates a bucket. A LocationConstraint matching the client's
// region is sent unless the region is us-east-1.
func (c *Client) CreateBucket(ctx context.Context, in *CreateBucketInput) error {
	r := &Request{
		Method: http.MethodPut,
		Bucket: in.Bucket,
	}

	if !in.NoLocationConstraint {
		body, err := xml.Marshal(createBucketConfiguration{
			XMLNS:              s3XmlNs,
			LocationConstraint: c.region,
		})
		if err != nil {
			return err
		}

		r.BodyPayload = body
		r.Header = http.Header{
			"Content-Type": {"application/xml"},
		}
	}

	_, err := c.DoClose(ctx, r)

	return err
}

// DeleteBucketInput is the input to DeleteBucket.
type DeleteBucketInput struct {
	Bucket string
}

// DeleteBucket deletes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, in *DeleteBucketInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodDelete,
		Bucket: in.Bucket,
	})
	return err
}

// HeadBucketInput is the input to HeadBucket.
type HeadBucketInput struct {
	Bucket string
}

// HeadBucket checks that a bucket exists and is accessible. A missing bucket
// yields an error matching ErrNotFound.
func (c *Client) HeadBucket(ctx context.Context, in *HeadBucketInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodHead,
		Bucket: in.Bucket,
	})
	return err
}
