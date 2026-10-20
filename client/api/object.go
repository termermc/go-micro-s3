package api

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const metaPrefix = "X-Amz-Meta-"

// ObjectCustomMetadata is custom metadata to attach to objects.
// Keys must be lowercase and valid as HTTP headers.
// For example, "user-id" is valid but "user id" is not.
type ObjectCustomMetadata map[string]string

// setMetadata adds user metadata headers to h.
func setMetadata(h http.Header, md ObjectCustomMetadata) {
	for key, val := range md {
		h.Set(metaPrefix+key, val)
	}
}

// ObjectMeta holds the metadata returned by Client.GetObject and Client.HeadObject.
type ObjectMeta struct {
	ContentLength int64
	ContentType   string
	ContentRange  string
	ETag          string
	LastModified  time.Time

	// Metadata holds user metadata (x-amz-meta-*).
	// The keys must be lowercase.
	Metadata map[string]string
}

func parseObjectMeta(resp *http.Response) ObjectMeta {
	m := ObjectMeta{
		ContentLength: resp.ContentLength,
		ContentType:   resp.Header.Get("Content-Type"),
		ContentRange:  resp.Header.Get("Content-Range"),
		ETag:          resp.Header.Get("ETag"),
	}

	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		m.LastModified, _ = http.ParseTime(lm)
	}

	// For HEAD the transport reports ContentLength -1; take it from the header.
	if m.ContentLength < 0 {
		n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
		if err == nil {
			m.ContentLength = n
		}
	}

	for key, vals := range resp.Header {
		if len(vals) > 0 && strings.HasPrefix(key, metaPrefix) {
			if m.Metadata == nil {
				m.Metadata = make(map[string]string)
			}
			m.Metadata[strings.ToLower(key[len(metaPrefix):])] = vals[0]
		}
	}

	return m
}

// PutObjectInput is the input to Client.PutObject.
type PutObjectInput struct {
	Bucket string
	Key    string

	// Body is the io.Reader to read for the object content.
	// It must return EXACTLY ContentLength bytes.
	Body io.Reader

	// The length of the object, in bytes.
	ContentLength int64

	ContentType string

	// Custom metadata for the object, if any.
	Metadata ObjectCustomMetadata

	// SseC is the SSE-C key to use for encrypting the object, if any.
	SseC *SseCustomerKey
}

// PutObjectOutput is the result of Client.PutObject.
type PutObjectOutput struct {
	ETag string
}

// PutObject uploads an object in a single request.
func (c *Client) PutObject(ctx context.Context, in *PutObjectInput) (*PutObjectOutput, error) {
	if in.ContentLength < 0 {
		return nil, errors.New("s3: ContentLength must not be negative")
	}
	if in.ContentLength > 0 && in.Body == nil {
		return nil, errors.New("s3: Body is nil but ContentLength is non-zero")
	}

	h := http.Header{}
	if in.ContentType != "" {
		h.Set("Content-Type", in.ContentType)
	}

	setMetadata(h, in.Metadata)
	setSseC(h, in.SseC)

	resp, err := c.DoClose(ctx, &Request{
		Method:        http.MethodPut,
		Bucket:        in.Bucket,
		Key:           in.Key,
		Header:        h,
		BodyReader:    in.Body,
		BodyReaderLen: in.ContentLength,
	})
	if err != nil {
		return nil, err
	}

	return &PutObjectOutput{
		ETag: resp.Header.Get("ETag"),
	}, nil
}

// GetObjectRange is a range to read from an object.
type GetObjectRange struct {
	// The offset to start reading from.
	Offset int64

	// The end byte to read.
	// Inclusive.
	// Use -1 to read until the end of the object.
	End int64
}

// ToHeader returns an HTTP Range header based on this range.
func (r GetObjectRange) ToHeader() string {
	if r.End < 0 {
		return "bytes=" + strconv.FormatInt(r.Offset, 10) + "-"
	}

	return "bytes=" + strconv.FormatInt(r.Offset, 10) + "-" + strconv.FormatInt(r.End, 10)
}

// GetObjectInput is the input to Client.GetObject.
type GetObjectInput struct {
	Bucket string
	Key    string

	Range *GetObjectRange

	// RangeHeader is an HTTP RangeHeader header value, e.g. "bytes=0-99".
	// If not empty, overrides Range.
	RangeHeader string

	// SseC is the key the object is encrypted with, if any.
	// If the object is encrypted and this field is unset or incorrect, Client.GetObject will return an error.
	SseC *SseCustomerKey
}

// GetObjectOutput is the result of GetObject.
// Important: the caller must close Body.
type GetObjectOutput struct {
	ObjectMeta
	Body io.ReadCloser
}

// GetObject downloads an object.
// It is the caller's responsibility to close the response body.
func (c *Client) GetObject(ctx context.Context, in *GetObjectInput) (*GetObjectOutput, error) {
	h := http.Header{}
	if in.RangeHeader != "" {
		h.Set("Range", in.RangeHeader)
	} else if in.Range != nil {
		h.Set("Range", in.Range.ToHeader())
	}

	setSseC(h, in.SseC)

	resp, err := c.Do(ctx, &Request{
		Method: http.MethodGet,
		Bucket: in.Bucket,
		Key:    in.Key,
		Header: h,
	})
	if err != nil {
		return nil, err
	}

	return &GetObjectOutput{
		ObjectMeta: parseObjectMeta(resp),
		Body:       resp.Body,
	}, nil
}

// HeadObjectInput is the input to Client.HeadObject.
type HeadObjectInput struct {
	Bucket string
	Key    string
	SseC   *SseCustomerKey
}

// HeadObjectOutput is the result of Client.HeadObject.
type HeadObjectOutput struct {
	ObjectMeta
}

// HeadObject retrieves an object's metadata. A missing object yields an error matching ErrNotFound.
func (c *Client) HeadObject(ctx context.Context, in *HeadObjectInput) (*HeadObjectOutput, error) {
	h := http.Header{}
	setSseC(h, in.SseC)

	resp, err := c.DoClose(ctx, &Request{
		Method: http.MethodHead,
		Bucket: in.Bucket,
		Key:    in.Key,
		Header: h,
	})
	if err != nil {
		return nil, err
	}

	return &HeadObjectOutput{
		ObjectMeta: parseObjectMeta(resp),
	}, nil
}

// DeleteObjectInput is the input to Client.DeleteObject.
type DeleteObjectInput struct {
	Bucket string
	Key    string
}

// DeleteObject deletes an object. Deleting a missing object is not an error.
func (c *Client) DeleteObject(ctx context.Context, in *DeleteObjectInput) error {
	_, err := c.DoClose(ctx, &Request{
		Method: http.MethodDelete,
		Bucket: in.Bucket,
		Key:    in.Key,
	})
	return err
}

// MaxDeleteObjects is the maximum number of keys accepted by Client.DeleteObjects.
const MaxDeleteObjects = 1_000

// DeleteObjectsInput is the input to DeleteObjects.
type DeleteObjectsInput struct {
	Bucket string

	// Keys to delete.
	// See MaxDeleteObjects for the maximum.
	Keys []string
}

// DeleteObjectsOutput is the result of DeleteObjects.
type DeleteObjectsOutput struct {
	Deleted []string
	Errors  []DeleteError
}

// DeleteObjects deletes up to MaxDeleteObjects objects in one request.
// Per-key failures are reported in Errors rather than as an error.
// Up to MaxDeleteObjects keys may be specified.
func (c *Client) DeleteObjects(ctx context.Context, in *DeleteObjectsInput) (*DeleteObjectsOutput, error) {
	if len(in.Keys) == 0 {
		return &DeleteObjectsOutput{}, nil
	}
	if len(in.Keys) > MaxDeleteObjects {
		return nil, errors.New("s3: too many keys for DeleteObjects")
	}

	req := deleteRequest{
		XMLNS:   s3XmlNs,
		Objects: make([]deleteIdentifier, len(in.Keys)),
	}
	for i, k := range in.Keys {
		req.Objects[i].Key = k
	}

	body, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}

	sum := md5.Sum(body)

	resp, err := c.Do(ctx, &Request{
		Method: http.MethodPost,
		Bucket: in.Bucket,
		Query:  url.Values{"delete": {""}},
		Header: http.Header{
			"Content-Type": {"application/xml"},
			"Content-Md5":  {base64.StdEncoding.EncodeToString(sum[:])},
		},
		BodyPayload: body,
	})
	if err != nil {
		return nil, err
	}

	var res deleteResult
	if err = decodeXmlBody(resp, &res, false); err != nil {
		return nil, err
	}

	out := &DeleteObjectsOutput{
		Errors: res.Errors,
	}
	for _, d := range res.Deleted {
		out.Deleted = append(out.Deleted, d.Key)
	}

	return out, nil
}

// MetadataDirective controls whether Client.CopyObject keeps or replaces metadata.
type MetadataDirective string

const (
	// MetadataDirectiveCopy means the metadata, except for the version ID, remains unchanged.
	MetadataDirectiveCopy MetadataDirective = "COPY"

	// MetadataDirectiveReplace means all original metadata is replaced by the metadata you specify.
	MetadataDirectiveReplace MetadataDirective = "REPLACE"
)

// CopyObjectInput is the input to Client.CopyObject.
type CopyObjectInput struct {
	SrcBucket string
	SrcKey    string
	Bucket    string
	Key       string

	// MetadataDirective defaults to COPY.
	// ContentType and Metadata are only applied with REPLACE.
	MetadataDirective MetadataDirective

	ContentType string

	// Custom metadata for the object, if any.
	Metadata map[string]string

	// SrcSseC is the key the source object is encrypted with, if any.
	SrcSseC *SseCustomerKey

	// SseC is the key to encrypt the destination object with, if any.
	SseC *SseCustomerKey
}

// CopyObjectOutput is the result of Client.CopyObject.
type CopyObjectOutput struct {
	ETag         string
	LastModified time.Time
}

// CopyObject copies an object server-side.
func (c *Client) CopyObject(ctx context.Context, in *CopyObjectInput) (*CopyObjectOutput, error) {
	h := http.Header{}
	h.Set("X-Amz-Copy-Source", "/"+in.SrcBucket+"/"+UriEncode(in.SrcKey, false))
	if in.MetadataDirective != "" {
		h.Set("X-Amz-Metadata-Directive", string(in.MetadataDirective))
	}
	if in.ContentType != "" {
		h.Set("Content-Type", in.ContentType)
	}

	setMetadata(h, in.Metadata)
	setCopySourceSseC(h, in.SrcSseC)
	setSseC(h, in.SseC)

	resp, err := c.Do(ctx, &Request{Method: http.MethodPut, Bucket: in.Bucket, Key: in.Key, Header: h})
	if err != nil {
		return nil, err
	}

	var res copyObjectResult
	if err = decodeXmlBody(resp, &res, true); err != nil {
		return nil, err
	}

	return &CopyObjectOutput{
		ETag:         res.ETag,
		LastModified: res.LastModified,
	}, nil
}

// ListObjectsV2Input is the input to Client.ListObjectsV2.
type ListObjectsV2Input struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	StartAfter        string
	ContinuationToken string

	// MaxKeys limits the page size; 0 uses the server default (1,000).
	MaxKeys int
}

// ListObjectsV2Output is one page of Client.ListObjectsV2 results.
type ListObjectsV2Output struct {
	Contents              []Object
	CommonPrefixes        []string
	IsTruncated           bool
	NextContinuationToken string
	KeyCount              int
}

// ListObjectsV2 returns one page of objects in a bucket.
func (c *Client) ListObjectsV2(ctx context.Context, in *ListObjectsV2Input) (*ListObjectsV2Output, error) {
	q := url.Values{
		"list-type": {"2"},
	}
	if in.Prefix != "" {
		q.Set("prefix", in.Prefix)
	}
	if in.Delimiter != "" {
		q.Set("delimiter", in.Delimiter)
	}
	if in.StartAfter != "" {
		q.Set("start-after", in.StartAfter)
	}
	if in.ContinuationToken != "" {
		q.Set("continuation-token", in.ContinuationToken)
	}
	if in.MaxKeys > 0 {
		q.Set("max-keys", strconv.Itoa(in.MaxKeys))
	}

	resp, err := c.Do(ctx, &Request{
		Method: http.MethodGet,
		Bucket: in.Bucket,
		Query:  q,
	})
	if err != nil {
		return nil, err
	}

	var res listBucketResult
	if err = decodeXmlBody(resp, &res, false); err != nil {
		return nil, err
	}

	out := &ListObjectsV2Output{
		Contents:              res.Contents,
		IsTruncated:           res.IsTruncated,
		NextContinuationToken: res.NextContinuationToken,
		KeyCount:              res.KeyCount,
	}
	for _, p := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, p.Prefix)
	}

	return out, nil
}
