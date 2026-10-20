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
// Fields without a stated default must be specified.
type Config struct {
	// The base URL of the S3 service, e.g. "https://s3.example.com" or "http://127.0.0.1:3900".
	Endpoint string

	// The SigV4 signing region, e.g. "us-east-1" or "garage".
	Region string

	// The S3 access key ID.
	AccessKeyID string

	// The S3 secret access key.
	SecretAccessKey string

	// If true, virtual-hosted-style addressing (bucket.host/key) will be used.
	// Defaults to path-style (host/bucket/key).
	VirtualHost bool

	// The HTTP client to use for requests.
	// Defaults to http.DefaultClient.
	HttpClient *http.Client
}

// Client is a low-level S3 client. It is safe for concurrent use.
type Client struct {
	endpoint    *url.URL
	region      string
	virtualHost bool
	httpClient  *http.Client
	signer      *Signer
	now         func() time.Time
}

var errNoEndpoint = errors.New("s3: endpoint is required")
var errEndpointNoHost = errors.New("s3: endpoint has no host")
var errNoRegion = errors.New("s3: region is required")

// New creates a new Client with a config.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, errNoEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("s3: invalid endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("s3: endpoint scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errEndpointNoHost
	}
	if cfg.Region == "" {
		return nil, errNoRegion
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""

	httpClient := cfg.HttpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		endpoint:    u,
		region:      cfg.Region,
		virtualHost: cfg.VirtualHost,
		httpClient:  httpClient,
		signer:      NewSigner(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Region),
		now:         time.Now,
	}, nil
}

// request describes a single S3 API call.
// Its body is either bodyReader with bodyReaderLen as its size, or the in-memory bodyPayload.
// If both bodyReader and bodyPayload are nil, no body will be sent.
type request struct {
	method string
	bucket string
	key    string
	query  url.Values
	header http.Header

	bodyReader    io.Reader
	bodyReaderLen int64

	bodyPayload []byte
}

// mkNormalizedUrl returns a normalized object URL used for signing.
func (c *Client) mkNormalizedUrl(bucket string, key string) *url.URL {
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
	u.RawPath = UriEncode(path, false)

	return &u
}

// do signs and sends an S3 request.
// On a non-2XX response it returns an *Error and a nil response.
func (c *Client) do(ctx context.Context, r *request) (*http.Response, error) {
	u := c.mkNormalizedUrl(r.bucket, r.key)
	if len(r.query) > 0 {
		u.RawQuery = ToCanonicalQuery(r.query)
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
	case r.bodyPayload != nil:
		// In-memory payload.
		// We can hash it since it's already in memory.
		req.Body = io.NopCloser(bytes.NewReader(r.bodyPayload))
		req.ContentLength = int64(len(r.bodyPayload))
		payloadHash = hashHex(r.bodyPayload)

	case r.bodyReader != nil && r.bodyReaderLen > 0:
		// Streamed bodies are unsigned.
		// If we were to sign them, we'd have to buffer the entire body into memory.
		// We set the Content-Length header to prevent chunked encoding, which is unsupported by S3.
		req.Body = io.NopCloser(r.bodyReader)
		req.ContentLength = r.bodyReaderLen
		payloadHash = unsignedPayload

	default:
		req.Body = http.NoBody
		req.ContentLength = 0
		payloadHash = emptyPayloadHash
	}

	c.signer.AddSignatureHeaders(req, payloadHash, c.now())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer func() {
			_ = resp.Body.Close()
		}()

		return nil, parseError(resp)
	}

	return resp, nil
}

// doClose runs a request with do, returning any errors it had, and then closing the body.
// It does not consume the response body for successful requests.
func (c *Client) doClose(ctx context.Context, r *request) (*http.Response, error) {
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}

	_ = resp.Body.Close()
	return resp, nil
}
