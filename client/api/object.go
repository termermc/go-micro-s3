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

// setMetadata adds user metadata headers to h.
func setMetadata(h http.Header, md map[string]string) {
	for k, v := range md {
		h.Set(metaPrefix+k, v)
	}
}

// ObjectMeta holds the metadata returned by GetObject and HeadObject.
type ObjectMeta struct {
	ContentLength int64
	ContentType   string
	ContentRange  string
	ETag          string
	LastModified  time.Time
	// Metadata holds user metadata (x-amz-meta-*) with lowercase keys.
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
		if n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64); err == nil {
			m.ContentLength = n
		}
	}
	for k, vs := range resp.Header {
		if len(vs) > 0 && strings.HasPrefix(k, metaPrefix) {
			if m.Metadata == nil {
				m.Metadata = make(map[string]string)
			}
			m.Metadata[strings.ToLower(k[len(metaPrefix):])] = vs[0]
		}
	}
	return m
}

// PutObjectInput is the input to PutObject.
type PutObjectInput struct {
	Bucket string
	Key    string
	// Body is streamed to the server without buffering. It must yield
	// exactly ContentLength bytes. It may be nil if ContentLength is 0.
	Body          io.Reader
	ContentLength int64
	ContentType   string
	Metadata      map[string]string
	SSEC          *SSECustomerKey
}

// PutObjectOutput is the result of PutObject.
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
	setSSEC(h, in.SSEC)
	resp, err := c.doClose(ctx, &request{
		method:  http.MethodPut,
		bucket:  in.Bucket,
		key:     in.Key,
		header:  h,
		body:    in.Body,
		bodyLen: in.ContentLength,
	})
	if err != nil {
		return nil, err
	}
	return &PutObjectOutput{ETag: resp.Header.Get("ETag")}, nil
}

// GetObjectInput is the input to GetObject.
type GetObjectInput struct {
	Bucket string
	Key    string
	// Range is an HTTP Range header value, e.g. "bytes=0-99".
	Range string
	SSEC  *SSECustomerKey
}

// GetObjectOutput is the result of GetObject. The caller must close Body.
type GetObjectOutput struct {
	ObjectMeta
	Body io.ReadCloser
}

// GetObject downloads an object. The body is streamed from the server; the
// caller must close it.
func (c *Client) GetObject(ctx context.Context, in *GetObjectInput) (*GetObjectOutput, error) {
	h := http.Header{}
	if in.Range != "" {
		h.Set("Range", in.Range)
	}
	setSSEC(h, in.SSEC)
	resp, err := c.do(ctx, &request{method: http.MethodGet, bucket: in.Bucket, key: in.Key, header: h})
	if err != nil {
		return nil, err
	}
	return &GetObjectOutput{ObjectMeta: parseObjectMeta(resp), Body: resp.Body}, nil
}

// HeadObjectInput is the input to HeadObject.
type HeadObjectInput struct {
	Bucket string
	Key    string
	SSEC   *SSECustomerKey
}

// HeadObjectOutput is the result of HeadObject.
type HeadObjectOutput struct {
	ObjectMeta
}

// HeadObject retrieves an object's metadata. A missing object yields an error
// matching ErrNotFound.
func (c *Client) HeadObject(ctx context.Context, in *HeadObjectInput) (*HeadObjectOutput, error) {
	h := http.Header{}
	setSSEC(h, in.SSEC)
	resp, err := c.doClose(ctx, &request{method: http.MethodHead, bucket: in.Bucket, key: in.Key, header: h})
	if err != nil {
		return nil, err
	}
	return &HeadObjectOutput{ObjectMeta: parseObjectMeta(resp)}, nil
}

// DeleteObjectInput is the input to DeleteObject.
type DeleteObjectInput struct {
	Bucket string
	Key    string
}

// DeleteObject deletes an object. Deleting a missing object is not an error.
func (c *Client) DeleteObject(ctx context.Context, in *DeleteObjectInput) error {
	_, err := c.doClose(ctx, &request{method: http.MethodDelete, bucket: in.Bucket, key: in.Key})
	return err
}

// MaxDeleteObjects is the maximum number of keys accepted by DeleteObjects.
const MaxDeleteObjects = 1000

// DeleteObjectsInput is the input to DeleteObjects.
type DeleteObjectsInput struct {
	Bucket string
	// Keys to delete; at most MaxDeleteObjects.
	Keys []string
}

// DeleteObjectsOutput is the result of DeleteObjects.
type DeleteObjectsOutput struct {
	Deleted []string
	Errors  []DeleteError
}

// DeleteObjects deletes up to MaxDeleteObjects objects in one request.
// Per-key failures are reported in Errors rather than as an error.
func (c *Client) DeleteObjects(ctx context.Context, in *DeleteObjectsInput) (*DeleteObjectsOutput, error) {
	if len(in.Keys) == 0 {
		return &DeleteObjectsOutput{}, nil
	}
	if len(in.Keys) > MaxDeleteObjects {
		return nil, errors.New("s3: too many keys for DeleteObjects")
	}
	req := deleteRequest{XMLNS: s3XMLNS, Objects: make([]deleteIdentifier, len(in.Keys))}
	for i, k := range in.Keys {
		req.Objects[i].Key = k
	}
	body, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(body)
	resp, err := c.do(ctx, &request{
		method: http.MethodPost,
		bucket: in.Bucket,
		query:  url.Values{"delete": {""}},
		header: http.Header{
			"Content-Type": {"application/xml"},
			"Content-Md5":  {base64.StdEncoding.EncodeToString(sum[:])},
		},
		payload: body,
	})
	if err != nil {
		return nil, err
	}
	var res deleteResult
	if err := decodeXML(resp, &res); err != nil {
		return nil, err
	}
	out := &DeleteObjectsOutput{Errors: res.Errors}
	for _, d := range res.Deleted {
		out.Deleted = append(out.Deleted, d.Key)
	}
	return out, nil
}

// MetadataDirective controls whether CopyObject keeps or replaces metadata.
type MetadataDirective string

const (
	MetadataDirectiveCopy    MetadataDirective = "COPY"
	MetadataDirectiveReplace MetadataDirective = "REPLACE"
)

// CopyObjectInput is the input to CopyObject.
type CopyObjectInput struct {
	SrcBucket string
	SrcKey    string
	Bucket    string
	Key       string
	// MetadataDirective defaults to COPY. ContentType and Metadata are only
	// applied with REPLACE.
	MetadataDirective MetadataDirective
	ContentType       string
	Metadata          map[string]string
	// SrcSSEC is the key the source object is encrypted with, if any.
	SrcSSEC *SSECustomerKey
	// SSEC is the key to encrypt the destination object with, if any.
	SSEC *SSECustomerKey
}

// CopyObjectOutput is the result of CopyObject.
type CopyObjectOutput struct {
	ETag         string
	LastModified time.Time
}

// CopyObject copies an object server-side.
func (c *Client) CopyObject(ctx context.Context, in *CopyObjectInput) (*CopyObjectOutput, error) {
	h := http.Header{}
	h.Set("X-Amz-Copy-Source", "/"+in.SrcBucket+"/"+uriEncode(in.SrcKey, false))
	if in.MetadataDirective != "" {
		h.Set("X-Amz-Metadata-Directive", string(in.MetadataDirective))
	}
	if in.ContentType != "" {
		h.Set("Content-Type", in.ContentType)
	}
	setMetadata(h, in.Metadata)
	setCopySourceSSEC(h, in.SrcSSEC)
	setSSEC(h, in.SSEC)
	resp, err := c.do(ctx, &request{method: http.MethodPut, bucket: in.Bucket, key: in.Key, header: h})
	if err != nil {
		return nil, err
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	if err := parseEmbeddedError(resp.StatusCode, body); err != nil {
		return nil, err
	}
	var res copyObjectResult
	if err := xml.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	return &CopyObjectOutput{ETag: res.ETag, LastModified: res.LastModified}, nil
}

// ListObjectsV2Input is the input to ListObjectsV2.
type ListObjectsV2Input struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	StartAfter        string
	ContinuationToken string
	// MaxKeys limits the page size; 0 uses the server default (1000).
	MaxKeys int
}

// ListObjectsV2Output is one page of ListObjectsV2 results.
type ListObjectsV2Output struct {
	Contents              []Object
	CommonPrefixes        []string
	IsTruncated           bool
	NextContinuationToken string
	KeyCount              int
}

// ListObjectsV2 returns one page of objects in a bucket.
func (c *Client) ListObjectsV2(ctx context.Context, in *ListObjectsV2Input) (*ListObjectsV2Output, error) {
	q := url.Values{"list-type": {"2"}}
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
	resp, err := c.do(ctx, &request{method: http.MethodGet, bucket: in.Bucket, query: q})
	if err != nil {
		return nil, err
	}
	var res listBucketResult
	if err := decodeXML(resp, &res); err != nil {
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
