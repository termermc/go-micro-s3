package client

import (
	"context"
	"io"
	"net/http"

	"github.com/termermc/go-micro-s3/client/api"
)

// GetObjectOptions are optional parameters for Client.GetObject and Client.WriteObjectTo.
type GetObjectOptions struct {
	// Range is an HTTP Range header value, e.g. "bytes=0-99".
	Range string

	// SseC is the key the object was encrypted with, if any.
	SseC *api.SseCustomerKey

	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// GetObject opens an object for reading.
// It is the caller's responsibility to close the reader.
// opts may be nil.
func (c *Client) GetObject(
	ctx context.Context,
	bucket string,
	key string,
	opts *GetObjectOptions,
) (io.ReadCloser, ObjectInfo, error) {
	if opts == nil {
		opts = &GetObjectOptions{}
	}

	out, err := c.api.GetObject(ctx, &api.GetObjectInput{
		Bucket:       bucket,
		Key:          key,
		RangeHeader:  opts.Range,
		SseC:         opts.SseC,
		ExtraHeaders: opts.ExtraHeaders,
	})
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	return out.Body, objectInfoFromMeta(key, out.ObjectMeta), nil
}

// WriteObjectTo writes an object's contents to w and returns the number of bytes written.
// opts may be nil.
func (c *Client) WriteObjectTo(
	ctx context.Context,
	bucket string,
	key string,
	w io.Writer,
	opts *GetObjectOptions,
) (int64, error) {
	body, _, err := c.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return 0, err
	}

	defer func() {
		_ = body.Close()
	}()
	return io.Copy(w, body)
}
