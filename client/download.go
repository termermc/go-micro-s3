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
	// SSEC is the key the object was encrypted with, if any.
	SSEC *SSECustomerKey
}

// Download opens an object for reading. The body is streamed from the server;
// the caller must close it. opts may be nil.
func (c *Client) Download(ctx context.Context, bucket, key string, opts *DownloadOptions) (io.ReadCloser, ObjectInfo, error) {
	if opts == nil {
		opts = &DownloadOptions{}
	}
	out, err := c.api.GetObject(ctx, &api.GetObjectInput{
		Bucket: bucket,
		Key:    key,
		Range:  opts.Range,
		SseC:   opts.SSEC,
	})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return out.Body, objectInfoFromMeta(key, out.ObjectMeta), nil
}

// DownloadTo writes an object's contents to w and returns the number of bytes
// written. opts may be nil.
func (c *Client) DownloadTo(ctx context.Context, bucket, key string, w io.Writer, opts *DownloadOptions) (int64, error) {
	body, _, err := c.Download(ctx, bucket, key, opts)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	return io.Copy(w, body)
}
