// Package client is a high-level S3 client built on package api. It provides
// simple Upload, Download, Stat, List, Copy and Delete operations. Uploads are
// streamed and never buffered; large uploads are split into multipart parts
// that are read directly from the source.
package client

import (
	"errors"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// ErrNotFound matches (via errors.Is) errors caused by a missing bucket or
// object.
var ErrNotFound = api.ErrNotFound

// SSECustomerKey is a 256-bit SSE-C key. See api.SSECustomerKey.
type SSECustomerKey = api.SSECustomerKey

// NewSSECustomerKey creates an SSECustomerKey from a 32-byte slice.
func NewSSECustomerKey(b []byte) (SSECustomerKey, error) {
	return api.NewSSECustomerKey(b)
}

// Config is the connection configuration. See api.Config.
type Config = api.Config

const (
	defaultPartSize           = 16 << 20
	defaultMultipartThreshold = 64 << 20
	defaultConcurrency        = 4
)

// Client is a high-level S3 client. It is safe for concurrent use.
type Client struct {
	api                *api.Client
	partSize           int64
	multipartThreshold int64
	concurrency        int
}

// Option configures a Client.
type Option func(*Client)

// WithPartSize sets the multipart part size. Values below api.MinPartSize are
// raised to it. The default is 16 MiB.
func WithPartSize(n int64) Option {
	return func(c *Client) { c.partSize = max(n, api.MinPartSize) }
}

// WithMultipartThreshold sets the size above which Upload uses a multipart
// upload. The default is 64 MiB.
func WithMultipartThreshold(n int64) Option {
	return func(c *Client) { c.multipartThreshold = n }
}

// WithConcurrency sets how many parts are uploaded in parallel when the
// source implements io.ReaderAt. The default is 4.
func WithConcurrency(n int) Option {
	return func(c *Client) { c.concurrency = max(n, 1) }
}

// New creates a Client from cfg.
func New(cfg Config, opts ...Option) (*Client, error) {
	a, err := api.New(cfg)
	if err != nil {
		return nil, err
	}
	return NewFromAPI(a, opts...)
}

// NewFromAPI creates a Client that wraps an existing low-level client.
func NewFromAPI(a *api.Client, opts ...Option) (*Client, error) {
	if a == nil {
		return nil, errors.New("s3: nil api client")
	}
	c := &Client{
		api:                a,
		partSize:           defaultPartSize,
		multipartThreshold: defaultMultipartThreshold,
		concurrency:        defaultConcurrency,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// API returns the underlying low-level client.
func (c *Client) API() *api.Client {
	return c.api
}
