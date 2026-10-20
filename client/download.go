package client

import (
	"context"
	"io"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// DownloadOptions are optional parameters for Download and DownloadTo.
type DownloadOptions struct {
	// Range is an HTTP Range header value, e.g. "bytes=0-99".
	Range string

	// SseC is the key the object was encrypted with, if any.
	SseC *api.SseCustomerKey
}

// Download opens an object for reading.
// It is the caller's responsibility to close the reader.
// opts may be nil.
func (c *Client) Download(
	ctx context.Context,
	bucket string,
	key string,
	opts *DownloadOptions,
) (io.ReadCloser, ObjectInfo, error) {
	if opts == nil {
		opts = &DownloadOptions{}
	}

	out, err := c.api.GetObject(ctx, &api.GetObjectInput{
		Bucket: bucket,
		Key:    key,
		Range:  opts.Range,
		SseC:   opts.SseC,
	})
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	return out.Body, objectInfoFromMeta(key, out.ObjectMeta), nil
}

// DownloadTo writes an object's contents to w and returns the number of bytes written.
// opts may be nil.
func (c *Client) DownloadTo(
	ctx context.Context,
	bucket string,
	key string,
	w io.Writer,
	opts *DownloadOptions,
) (int64, error) {
	body, _, err := c.Download(ctx, bucket, key, opts)
	if err != nil {
		return 0, err
	}

	defer func() {
		_ = body.Close()
	}()
	return io.Copy(w, body)
}
