package api

import (
	"bytes"
	"context"
	"encoding/xml"
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
	AccessKeyId string

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
		signer:      NewSigner(cfg.AccessKeyId, cfg.SecretAccessKey, cfg.Region),
		now:         time.Now,
	}, nil
}

// Request is an S3 Request.
// Its body is either bodyReader with bodyReaderLen as its size, or the in-memory bodyPayload.
// If both bodyReader and bodyPayload are nil, no body will be sent.
type Request struct {
	// The HTTP request method.
	Method string

	// The Bucket, if any.
	Bucket string

	// The Key, if any.
	Key string

	// The query parameters to include, if any.
	Query url.Values

	// The extra headers to include, if any.
	Header http.Header

	// If not nil, the request body will be streamed from this reader.
	// If set, BodyReaderLen must also be set.
	BodyReader io.Reader

	// If not zero, the request body will be streamed from BodyReader.
	// If set, BodyReader must also be set.
	BodyReaderLen int64

	// If not nil, this payload will be sent as the request body.
	// If both BodyReader and BodyPayload are set, BodyPayload will be used.
	BodyPayload []byte
}

// newHeader returns a copy of extra with canonicalized keys, for a call to add its own headers to.
// Headers the call then sets with http.Header.Set replace extra headers of the same name.
func newHeader(extra http.Header) http.Header {
	h := make(http.Header, len(extra))
	for key, vals := range extra {
		key = http.CanonicalHeaderKey(key)
		h[key] = append(h[key], vals...)
	}

	return h
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

// Do signs and sends an S3 Request.
// On a non-2XX response it returns an *Error and a nil response.
func (c *Client) Do(ctx context.Context, r *Request) (*http.Response, error) {
	u := c.mkNormalizedUrl(r.Bucket, r.Key)
	if len(r.Query) > 0 {
		u.RawQuery = ToCanonicalQuery(r.Query)
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for key, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}

	switch {
	case r.BodyPayload != nil:
		req.Body = io.NopCloser(bytes.NewReader(r.BodyPayload))
		req.ContentLength = int64(len(r.BodyPayload))

	case r.BodyReader != nil && r.BodyReaderLen > 0:
		req.Body = io.NopCloser(r.BodyReader)
		req.ContentLength = r.BodyReaderLen

	default:
		req.Body = http.NoBody
		req.ContentLength = 0
	}

	c.signer.AddSignatureHeaders(req, unsignedPayload, c.now())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		const maxErrorBody = 64 * 1024 // 64KiB

		defer func() {
			_ = resp.Body.Close()
		}()

		e := &Error{
			StatusCode: resp.StatusCode,
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if len(body) > 0 {
			_ = xml.Unmarshal(body, e)
		}

		if e.Code == "" {
			e.Code = codeForStatus(resp.StatusCode)
		}
		if e.RequestId == "" {
			e.RequestId = resp.Header.Get("X-Amz-Request-Id")
		}

		return nil, e
	}

	return resp, nil
}

// DoClose runs a Request with Do, returning any errors it had, and then closing the body.
// It does not consume the response body for successful responses.
func (c *Client) DoClose(ctx context.Context, r *Request) (*http.Response, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return nil, err
	}

	_ = resp.Body.Close()

	return resp, nil
}
