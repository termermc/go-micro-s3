package api

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const (
	// MinPartSize is the minimum allowed part size for multipart uploads.
	MinPartSize = 5 * 1024 * 1024 // 5MiB

	// MaxPartSize is the maximum allowed part size for multipart uploads.
	MaxPartSize = 5 * 1024 * 1024 * 1024 // 50MiB

	// MaxParts is the maximum allowed number of parts for multipart uploads.
	MaxParts = 10_000
)

// CreateMultipartUploadInput is the input to CreateMultipartUpload.
type CreateMultipartUploadInput struct {
	Bucket      string
	Key         string
	ContentType string

	// Custom metadata for the object, if any.
	Metadata ObjectCustomMetadata

	// SseC is the SSE-C key to use for encrypting the object, if any.
	// If not nil, all UploadPart calls for this upload must be made with the same key.
	SseC *SseCustomerKey
}

// CreateMultipartUploadOutput is the result of CreateMultipartUpload.
type CreateMultipartUploadOutput struct {
	UploadId string
}

// CreateMultipartUpload starts a multipart upload.
func (c *Client) CreateMultipartUpload(
	ctx context.Context,
	in *CreateMultipartUploadInput,
) (*CreateMultipartUploadOutput, error) {
	h := http.Header{}
	if in.ContentType != "" {
		h.Set("Content-Type", in.ContentType)
	}
	setMetadata(h, in.Metadata)
	setSseC(h, in.SseC)

	resp, err := c.Do(ctx, &Request{
		Method: http.MethodPost,
		Bucket: in.Bucket,
		Key:    in.Key,
		Query:  url.Values{"uploads": {""}},
		Header: h,
	})
	if err != nil {
		return nil, err
	}

	var res initiateMultipartUploadResult
	if err = decodeXmlBody(resp, &res, false); err != nil {
		return nil, err
	}

	if res.UploadId == "" {
		return nil, errors.New("s3: CreateMultipartUpload returned no upload ID")
	}

	return &CreateMultipartUploadOutput{
		UploadId: res.UploadId,
	}, nil
}

// UploadPartInput is the input to UploadPart.
type UploadPartInput struct {
	Bucket   string
	Key      string
	UploadId string

	// PartNumber must be in the range [1, MaxParts].
	PartNumber int

	// Body is the io.Reader to read for the part content.
	// It must return EXACTLY ContentLength bytes.
	Body io.Reader

	// The length of the part, in bytes.
	ContentLength int64

	// SseC is the SSE-C key to use for encrypting the object, if any.
	// This value must match the one passed to Client.CreateMultipartUpload.
	SseC *SseCustomerKey
}

// UploadPartOutput is the result of UploadPart.
type UploadPartOutput struct {
	ETag string
}

// UploadPart uploads one part of a multipart upload.
func (c *Client) UploadPart(ctx context.Context, in *UploadPartInput) (*UploadPartOutput, error) {
	if in.PartNumber < 1 || in.PartNumber > MaxParts {
		return nil, errors.New("s3: part number out of range")
	}
	if in.ContentLength < 0 {
		return nil, errors.New("s3: ContentLength must not be negative")
	}
	if in.ContentLength > 0 && in.Body == nil {
		return nil, errors.New("s3: Body is nil but ContentLength is non-zero")
	}

	h := http.Header{}
	setSseC(h, in.SseC)

	resp, err := c.DoClose(ctx, &Request{
		Method: http.MethodPut,
		Bucket: in.Bucket,
		Key:    in.Key,
		Query: url.Values{
			"partNumber": {strconv.Itoa(in.PartNumber)},
			"uploadId":   {in.UploadId},
		},
		Header:        h,
		BodyReader:    in.Body,
		BodyReaderLen: in.ContentLength,
	})
	if err != nil {
		return nil, err
	}

	return &UploadPartOutput{
		ETag: resp.Header.Get("ETag"),
	}, nil
}

// CompleteMultipartUploadInput is the input to CompleteMultipartUpload.
type CompleteMultipartUploadInput struct {
	Bucket   string
	Key      string
	UploadId string

	// Parts are the upload parts that make up the full upload.
	// They must be in ascending PartNumber order.
	// The ETag in each part come from Client.UploadPart responses.
	Parts []CompletedPart
}

// CompleteMultipartUploadOutput is the result of CompleteMultipartUpload.
type CompleteMultipartUploadOutput struct {
	ETag     string
	Location string
}

// CompleteMultipartUpload assembles previously uploaded parts into an object.
func (c *Client) CompleteMultipartUpload(ctx context.Context, in *CompleteMultipartUploadInput) (*CompleteMultipartUploadOutput, error) {
	body, err := xml.Marshal(completeMultipartUpload{XMLNS: s3XmlNs, Parts: in.Parts})
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, &Request{
		Method: http.MethodPost,
		Bucket: in.Bucket,
		Key:    in.Key,
		Query: url.Values{
			"uploadId": {in.UploadId},
		},
		Header: http.Header{
			"Content-Type": {"application/xml"},
		},
		BodyPayload: body,
	})
	if err != nil {
		return nil, err
	}

	var res completeMultipartUploadResult
	if err = decodeXmlBody(resp, &res, true); err != nil {
		return nil, err
	}

	return &CompleteMultipartUploadOutput{
		ETag:     res.ETag,
		Location: res.Location,
	}, nil
}

// AbortMultipartUploadInput is the input to AbortMultipartUpload.
type AbortMultipartUploadInput struct {
	Bucket   string
	Key      string
	UploadId string
}

// AbortMultipartUpload aborts a multipart upload and discards its parts.
func (c *Client) AbortMultipartUpload(ctx context.Context, in *AbortMultipartUploadInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodDelete,
		Bucket: in.Bucket,
		Key:    in.Key,
		Query:  url.Values{"uploadId": {in.UploadId}},
	})
	return err
}

// ListMultipartUploadsInput is the input to ListMultipartUploads.
type ListMultipartUploadsInput struct {
	Bucket         string
	Prefix         string
	KeyMarker      string
	UploadIdMarker string
	// MaxUploads limits the page size; 0 uses the server default (1000).
	MaxUploads int
}

// ListMultipartUploadsOutput is one page of in-progress multipart uploads.
type ListMultipartUploadsOutput struct {
	Uploads            []MultipartUpload
	IsTruncated        bool
	NextKeyMarker      string
	NextUploadIDMarker string
}

// ListMultipartUploads returns one page of in-progress multipart uploads.
func (c *Client) ListMultipartUploads(ctx context.Context, in *ListMultipartUploadsInput) (*ListMultipartUploadsOutput, error) {
	q := url.Values{
		"uploads": {""},
	}
	if in.Prefix != "" {
		q.Set("prefix", in.Prefix)
	}
	if in.KeyMarker != "" {
		q.Set("key-marker", in.KeyMarker)
	}
	if in.UploadIdMarker != "" {
		q.Set("upload-id-marker", in.UploadIdMarker)
	}
	if in.MaxUploads > 0 {
		q.Set("max-uploads", strconv.Itoa(in.MaxUploads))
	}
	resp, err := c.Do(ctx, &Request{Method: http.MethodGet, Bucket: in.Bucket, Query: q})
	if err != nil {
		return nil, err
	}
	var res listMultipartUploadsResult
	if err = decodeXmlBody(resp, &res, false); err != nil {
		return nil, err
	}
	return &ListMultipartUploadsOutput{
		Uploads:            res.Uploads,
		IsTruncated:        res.IsTruncated,
		NextKeyMarker:      res.NextKeyMarker,
		NextUploadIDMarker: res.NextUploadIdMarker,
	}, nil
}
