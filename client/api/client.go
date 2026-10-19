// Package api is a low-level S3 client. Each method maps to exactly one S3 API
// call. Request bodies are streamed directly to the server and are never
// buffered; they are sent with the UNSIGNED-PAYLOAD SigV4 payload hash.
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config holds the settings needed to talk to an S3 endpoint.
type Config struct {
	// Endpoint is the base URL of the S3 service, e.g. "https://s3.example.com"
	// or "http://127.0.0.1:3900".
	Endpoint string
	// Region is the SigV4 signing region, e.g. "us-east-1" or "garage".
	Region string
	// AccessKeyID and SecretAccessKey are the credentials used for signing.
	AccessKeyID     string
	SecretAccessKey string
	// VirtualHost selects virtual-hosted-style addressing
	// (bucket.host/key). The default is path-style (host/bucket/key).
	VirtualHost bool
	// HTTPClient is the HTTP client used for requests. Defaults to
	// http.DefaultClient.
	HTTPClient *http.Client
}

// Client is a low-level S3 client. It is safe for concurrent use.
type Client struct {
	endpoint    *url.URL
	region      string
	virtualHost bool
	httpClient  *http.Client
	signer      *signer
	now         func() time.Time
}

// New creates a Client from cfg.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("s3: endpoint is required")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("s3: invalid endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("s3: endpoint scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("s3: endpoint has no host")
	}
	if cfg.Region == "" {
		return nil, errors.New("s3: region is required")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""

	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		endpoint:    u,
		region:      cfg.Region,
		virtualHost: cfg.VirtualHost,
		httpClient:  hc,
		signer:      newSigner(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Region),
		now:         time.Now,
	}, nil
}

// request describes a single S3 API call.
type request struct {
	method string
	bucket string
	key    string
	query  url.Values
	header http.Header

	// body is either a streamed upload (bodyLen bytes, sent unsigned) or nil.
	body    io.Reader
	bodyLen int64
	// payload is an in-memory body (e.g. XML) whose hash is signed. Mutually
	// exclusive with body.
	payload []byte
}

// buildURL returns the request URL and the escaped path used for signing.
func (c *Client) buildURL(bucket, key string) *url.URL {
	u := *c.endpoint
	path := c.endpoint.Path
	if bucket != "" {
		if c.virtualHost {
			u.Host = bucket + "." + u.Host
		} else {
			path += "/" + bucket
		}
	}
	if key != "" {
		path += "/" + key
	}
	if path == "" {
		path = "/"
	}
	u.Path = path
	u.RawPath = uriEncode(path, false)
	return &u
}

// do signs and sends r. On a non-2xx response it returns an *Error and
// closes the body. On success the caller owns resp.Body.
func (c *Client) do(ctx context.Context, r *request) (*http.Response, error) {
	u := c.buildURL(r.bucket, r.key)
	if len(r.query) > 0 {
		u.RawQuery = canonicalQuery(r.query)
	}

	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	var payloadHash string
	switch {
	case r.payload != nil:
		req.Body = io.NopCloser(bytes.NewReader(r.payload))
		req.ContentLength = int64(len(r.payload))
		payloadHash = hashHex(r.payload)
	case r.body != nil && r.bodyLen > 0:
		// Streamed upload: no GetBody, so net/http never needs to replay
		// (and thus buffer) the body. A fixed ContentLength prevents chunked
		// transfer encoding, which S3 does not accept.
		req.Body = io.NopCloser(r.body)
		req.ContentLength = r.bodyLen
		payloadHash = unsignedPayload
	default:
		req.Body = http.NoBody
		req.ContentLength = 0
		payloadHash = emptyPayloadHash
	}

	c.signer.sign(req, payloadHash, c.now())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp, nil
}

// doClose performs r and discards the response body.
func (c *Client) doClose(ctx context.Context, r *request) (*http.Response, error) {
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp, nil
}
