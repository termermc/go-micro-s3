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

// ListBucketsInput is the input to Client.ListBuckets.
type ListBucketsInput struct {
	// ExtraHeaders are additional headers to send with the request, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// ListBuckets lists all buckets owned by the authenticated user.
// in may be nil.
func (c *Client) ListBuckets(ctx context.Context, in *ListBucketsInput) (*ListBucketsOutput, error) {
	if in == nil {
		in = &ListBucketsInput{}
	}

	resp, err := c.Do(ctx, &Request{
		Method: http.MethodGet,
		Header: newHeader(in.ExtraHeaders),
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

	// ExtraHeaders are additional headers to send with the request, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// CreateBucket creates a bucket. A LocationConstraint matching the client's
// region is sent unless the region is us-east-1.
func (c *Client) CreateBucket(ctx context.Context, in *CreateBucketInput) error {
	r := &Request{
		Method: http.MethodPut,
		Bucket: in.Bucket,
		Header: newHeader(in.ExtraHeaders),
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
		r.Header.Set("Content-Type", "application/xml")
	}

	_, err := c.DoClose(ctx, r)

	return err
}

// DeleteBucketInput is the input to DeleteBucket.
type DeleteBucketInput struct {
	Bucket string

	// ExtraHeaders are additional headers to send with the request, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// DeleteBucket deletes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, in *DeleteBucketInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodDelete,
		Bucket: in.Bucket,
		Header: newHeader(in.ExtraHeaders),
	})
	return err
}

// HeadBucketInput is the input to HeadBucket.
type HeadBucketInput struct {
	Bucket string

	// ExtraHeaders are additional headers to send with the request, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// HeadBucket checks that a bucket exists and is accessible. A missing bucket
// yields an error matching ErrNotFound.
func (c *Client) HeadBucket(ctx context.Context, in *HeadBucketInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodHead,
		Bucket: in.Bucket,
		Header: newHeader(in.ExtraHeaders),
	})
	return err
}
