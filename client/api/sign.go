package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	signingAlgorithm = "AWS4-HMAC-SHA256"
	unsignedPayload  = "UNSIGNED-PAYLOAD"
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	amzDateFormat    = "20060102T150405Z"
	shortDateFormat  = "20060102"
)

// signer implements AWS Signature Version 4 for S3.
type signer struct {
	accessKey string
	secretKey string
	region    string

	mu      sync.Mutex
	keyDate string
	key     []byte
}

func newSigner(accessKey, secretKey, region string) *signer {
	return &signer{accessKey: accessKey, secretKey: secretKey, region: region}
}

// sign adds the x-amz-date, x-amz-content-sha256 and Authorization headers to
// req. payloadHash is the hex SHA-256 of the body, or UNSIGNED-PAYLOAD.
func (s *signer) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(amzDateFormat)
	shortDate := now.Format(shortDateFormat)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	signedHeaders, canonicalHeaders := canonicalHeaders(req.Header, host)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := signingAlgorithm + "\n" + amzDate + "\n" + scope + "\n" + hashHex([]byte(canonicalRequest))

	signature := hex.EncodeToString(hmacSHA256(s.signingKey(shortDate), stringToSign))
	req.Header.Set("Authorization", signingAlgorithm+
		" Credential="+s.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+
		", Signature="+signature)
}

// signingKey returns the derived signing key for date, caching it per day.
func (s *signer) signingKey(date string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyDate != date {
		k := hmacSHA256([]byte("AWS4"+s.secretKey), date)
		k = hmacSHA256(k, s.region)
		k = hmacSHA256(k, "s3")
		s.key = hmacSHA256(k, "aws4_request")
		s.keyDate = date
	}
	return s.key
}

// isSignedHeader reports whether the lowercase header name is included in the
// signature. host and all x-amz-* headers are required; a few standard
// headers are included because S3 recommends signing them.
func isSignedHeader(name string) bool {
	switch name {
	case "content-md5", "content-type", "date", "range":
		return true
	}
	return strings.HasPrefix(name, "x-amz-")
}

// canonicalHeaders returns the SignedHeaders list and the CanonicalHeaders
// block (with its trailing newline) for h plus the host header.
func canonicalHeaders(h http.Header, host string) (signed, canonical string) {
	vals := map[string]string{"host": host}
	names := []string{"host"}
	for k, vs := range h {
		name := strings.ToLower(k)
		if !isSignedHeader(name) {
			continue
		}
		trimmed := make([]string, len(vs))
		for i, v := range vs {
			trimmed[i] = strings.Join(strings.Fields(v), " ")
		}
		vals[name] = strings.Join(trimmed, ",")
		names = append(names, name)
	}
	slices.Sort(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(vals[n])
		b.WriteByte('\n')
	}
	return strings.Join(names, ";"), b.String()
}

// canonicalQuery encodes q with keys sorted and values URI-encoded as required
// by SigV4. The result is also used as the request's raw query.
func canonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		ek := uriEncode(k, true)
		if len(vs) == 0 {
			pairs = append(pairs, ek+"=")
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, ek+"="+uriEncode(v, true))
		}
	}
	slices.Sort(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode percent-encodes every byte of s except the unreserved characters
// A-Z, a-z, 0-9, '-', '.', '_' and '~'. '/' is left alone unless encodeSlash
// is set.
func uriEncode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xF])
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
