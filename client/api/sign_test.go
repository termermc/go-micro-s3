package api

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Verbatim examples from https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html
const (
	exampleAccessKey = "AKIAIOSFODNN7EXAMPLE"
	exampleSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestSignAWSExamples(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		url           string
		header        http.Header
		payloadHash   string
		signedHeaders string
		signature     string
	}{
		{
			name:          "GET object with range",
			method:        http.MethodGet,
			url:           "https://examplebucket.s3.amazonaws.com/test.txt",
			header:        http.Header{"Range": {"bytes=0-9"}},
			payloadHash:   emptyPayloadHash,
			signedHeaders: "host;range;x-amz-content-sha256;x-amz-date",
			signature:     "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name:   "PUT object",
			method: http.MethodPut,
			url:    "https://examplebucket.s3.amazonaws.com/test$file.text",
			header: http.Header{
				"Date":                {"Fri, 24 May 2013 00:00:00 GMT"},
				"X-Amz-Storage-Class": {"REDUCED_REDUNDANCY"},
			},
			payloadHash:   "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072",
			signedHeaders: "date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
			signature:     "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
		{
			name:          "GET bucket lifecycle",
			method:        http.MethodGet,
			url:           "https://examplebucket.s3.amazonaws.com/?lifecycle=",
			payloadHash:   emptyPayloadHash,
			signedHeaders: "host;x-amz-content-sha256;x-amz-date",
			signature:     "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
		},
		{
			name:          "GET bucket list objects",
			method:        http.MethodGet,
			url:           "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			payloadHash:   emptyPayloadHash,
			signedHeaders: "host;x-amz-content-sha256;x-amz-date",
			signature:     "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			u.RawPath = UriEncode(u.Path, false)
			u.RawQuery = ToCanonicalQuery(u.Query())
			req := &http.Request{Method: tt.method, URL: u, Host: u.Host, Header: http.Header{}}
			maps.Copy(req.Header, tt.header)

			NewSigner(exampleAccessKey, exampleSecretKey, "us-east-1").AddSignatureHeaders(req, tt.payloadHash, exampleTime)

			want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
				" SignedHeaders=" + tt.signedHeaders + ", Signature=" + tt.signature
			if got := req.Header.Get("Authorization"); got != want {
				t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestURIEncode(t *testing.T) {
	tests := []struct {
		in          string
		encodeSlash bool
		want        string
	}{
		{"/bucket/a b+c", false, "/bucket/a%20b%2Bc"},
		{"/a//b/~x_y-z.txt", false, "/a//b/~x_y-z.txt"},
		{"a/b", true, "a%2Fb"},
		{"100%=&?#", false, "100%25%3D%26%3F%23"},
		{"日本", false, "%E6%97%A5%E6%9C%AC"},
	}
	for _, tt := range tests {
		if got := UriEncode(tt.in, tt.encodeSlash); got != tt.want {
			t.Errorf("uriEncode(%q, %v) = %q, want %q", tt.in, tt.encodeSlash, got, tt.want)
		}
	}
}

func TestBuildURLKeepsEncoding(t *testing.T) {
	c, err := New(Config{
		Endpoint: "http://127.0.0.1:3900/",
		Region:   "garage",
	})
	if err != nil {
		t.Fatal(err)
	}
	u := c.mkNormalizedUrl("bkt", "dir/a b+c%d")
	if got, want := u.String(), "http://127.0.0.1:3900/bkt/dir/a%20b%2Bc%25d"; got != want {
		t.Errorf("path-style URL = %q, want %q", got, want)
	}

	c.virtualHost = true
	u = c.mkNormalizedUrl("bkt", "k")
	if got, want := u.String(), "http://bkt.127.0.0.1:3900/k"; got != want {
		t.Errorf("vhost URL = %q, want %q", got, want)
	}
	if !strings.HasPrefix(c.mkNormalizedUrl("", "").String(), "http://127.0.0.1:3900/") {
		t.Errorf("service URL = %q", c.mkNormalizedUrl("", "").String())
	}
}
