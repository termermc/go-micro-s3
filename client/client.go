package client

import (
	"errors"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// Config holds the settings needed to talk to an S3 endpoint.
// Fields without a stated default must be specified.
type Config = api.Config

const (
	// DefaultPartSize is the default part size to use for multipart uploads.
	DefaultPartSize = 16 * 1024 * 1024

	// DefaultMultipartThreshold is the file size threshold before uploads will be multipart.
	DefaultMultipartThreshold = 64 * 1024 * 1024 // 64MiB

	// DefaultMultipartConcurrency is the default concurrency to use for multipart uploads, if possible.
	DefaultMultipartConcurrency = 4
)

// Client is a high-level S3 client. It is safe for concurrent use.
type Client struct {
	api *api.Client

	partSize           int64
	multipartThreshold int64
	concurrency        int
}

// Option configures a Client.
type Option func(*Client)

// WithPartSize sets the multipart part size. Values below api.MinPartSize are raised to it.
// The default is DefaultPartSize.
func WithPartSize(n int64) Option {
	return func(c *Client) {
		c.partSize = max(n, api.MinPartSize)
	}
}

// WithMultipartThreshold sets the size above which Client.Upload uses a multipart upload.
// The default is DefaultMultipartThreshold.
func WithMultipartThreshold(n int64) Option {
	return func(c *Client) {
		c.multipartThreshold = n
	}
}

// WithConcurrency sets how many parts are uploaded in parallel when the source implements io.ReaderAt.
// The default is DefaultMultipartConcurrency.
func WithConcurrency(n int) Option {
	return func(c *Client) { c.concurrency = max(n, 1) }
}

// New creates a Client from cfg.
func New(cfg Config, opts ...Option) (*Client, error) {
	a, err := api.New(cfg)
	if err != nil {
		return nil, err
	}

	return NewFromApi(a, opts...)
}

// NewFromApi creates a new Client that wraps an existing low-level client.
func NewFromApi(a *api.Client, opts ...Option) (*Client, error) {
	if a == nil {
		return nil, errors.New("s3: nil api client")
	}

	c := &Client{
		api:                a,
		partSize:           DefaultPartSize,
		multipartThreshold: DefaultMultipartThreshold,
		concurrency:        DefaultMultipartConcurrency,
	}
	for _, o := range opts {
		o(c)
	}

	return c, nil
}

// Api returns the underlying low-level client.
func (c *Client) Api() *api.Client {
	return c.api
}
