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

// Multipart upload limits imposed by S3.
const (
	MinPartSize = 5 << 20
	MaxPartSize = 5 << 30
	MaxParts    = 10000
)

// CreateMultipartUploadInput is the input to CreateMultipartUpload.
type CreateMultipartUploadInput struct {
	Bucket      string
	Key         string
	ContentType string
	Metadata    map[string]string
	// SSEC must also be passed to every UploadPart call.
	SSEC *SSECustomerKey
}

// CreateMultipartUploadOutput is the result of CreateMultipartUpload.
type CreateMultipartUploadOutput struct {
	UploadID string
}

// CreateMultipartUpload starts a multipart upload.
func (c *Client) CreateMultipartUpload(ctx context.Context, in *CreateMultipartUploadInput) (*CreateMultipartUploadOutput, error) {
	h := http.Header{}
	if in.ContentType != "" {
		h.Set("Content-Type", in.ContentType)
	}
	setMetadata(h, in.Metadata)
	setSSEC(h, in.SSEC)
	resp, err := c.do(ctx, &request{
		method: http.MethodPost,
		bucket: in.Bucket,
		key:    in.Key,
		query:  url.Values{"uploads": {""}},
		header: h,
	})
	if err != nil {
		return nil, err
	}
	var res initiateMultipartUploadResult
	if err := decodeXML(resp, &res); err != nil {
		return nil, err
	}
	if res.UploadID == "" {
		return nil, errors.New("s3: CreateMultipartUpload returned no upload ID")
	}
	return &CreateMultipartUploadOutput{UploadID: res.UploadID}, nil
}

// UploadPartInput is the input to UploadPart.
type UploadPartInput struct {
	Bucket   string
	Key      string
	UploadID string
	// PartNumber is in the range [1, MaxParts].
	PartNumber int
	// Body is streamed to the server without buffering. It must yield
	// exactly ContentLength bytes.
	Body          io.Reader
	ContentLength int64
	// SSEC must match the key given to CreateMultipartUpload.
	SSEC *SSECustomerKey
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
	setSSEC(h, in.SSEC)
	resp, err := c.doClose(ctx, &request{
		method: http.MethodPut,
		bucket: in.Bucket,
		key:    in.Key,
		query: url.Values{
			"partNumber": {strconv.Itoa(in.PartNumber)},
			"uploadId":   {in.UploadID},
		},
		header:  h,
		body:    in.Body,
		bodyLen: in.ContentLength,
	})
	if err != nil {
		return nil, err
	}
	return &UploadPartOutput{ETag: resp.Header.Get("ETag")}, nil
}

// CompleteMultipartUploadInput is the input to CompleteMultipartUpload.
type CompleteMultipartUploadInput struct {
	Bucket   string
	Key      string
	UploadID string
	// Parts must be in ascending PartNumber order.
	Parts []CompletedPart
}

// CompleteMultipartUploadOutput is the result of CompleteMultipartUpload.
type CompleteMultipartUploadOutput struct {
	ETag     string
	Location string
}

// CompleteMultipartUpload assembles previously uploaded parts into an object.
func (c *Client) CompleteMultipartUpload(ctx context.Context, in *CompleteMultipartUploadInput) (*CompleteMultipartUploadOutput, error) {
	body, err := xml.Marshal(completeMultipartUpload{XMLNS: s3XMLNS, Parts: in.Parts})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, &request{
		method:  http.MethodPost,
		bucket:  in.Bucket,
		key:     in.Key,
		query:   url.Values{"uploadId": {in.UploadID}},
		header:  http.Header{"Content-Type": {"application/xml"}},
		payload: body,
	})
	if err != nil {
		return nil, err
	}
	respBody, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	if err := parseEmbeddedError(resp.StatusCode, respBody); err != nil {
		return nil, err
	}
	var res completeMultipartUploadResult
	if err := xml.Unmarshal(respBody, &res); err != nil {
		return nil, err
	}
	return &CompleteMultipartUploadOutput{ETag: res.ETag, Location: res.Location}, nil
}

// AbortMultipartUploadInput is the input to AbortMultipartUpload.
type AbortMultipartUploadInput struct {
	Bucket   string
	Key      string
	UploadID string
}

// AbortMultipartUpload aborts a multipart upload and discards its parts.
func (c *Client) AbortMultipartUpload(ctx context.Context, in *AbortMultipartUploadInput) error {
	_, err := c.doClose(ctx, &request{
		method: http.MethodDelete,
		bucket: in.Bucket,
		key:    in.Key,
		query:  url.Values{"uploadId": {in.UploadID}},
	})
	return err
}

// ListMultipartUploadsInput is the input to ListMultipartUploads.
type ListMultipartUploadsInput struct {
	Bucket         string
	Prefix         string
	KeyMarker      string
	UploadIDMarker string
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
	q := url.Values{"uploads": {""}}
	if in.Prefix != "" {
		q.Set("prefix", in.Prefix)
	}
	if in.KeyMarker != "" {
		q.Set("key-marker", in.KeyMarker)
	}
	if in.UploadIDMarker != "" {
		q.Set("upload-id-marker", in.UploadIDMarker)
	}
	if in.MaxUploads > 0 {
		q.Set("max-uploads", strconv.Itoa(in.MaxUploads))
	}
	resp, err := c.do(ctx, &request{method: http.MethodGet, bucket: in.Bucket, query: q})
	if err != nil {
		return nil, err
	}
	var res listMultipartUploadsResult
	if err := decodeXML(resp, &res); err != nil {
		return nil, err
	}
	return &ListMultipartUploadsOutput{
		Uploads:            res.Uploads,
		IsTruncated:        res.IsTruncated,
		NextKeyMarker:      res.NextKeyMarker,
		NextUploadIDMarker: res.NextUploadIDMarker,
	}, nil
}
