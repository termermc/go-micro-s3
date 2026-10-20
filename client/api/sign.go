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
	"unsafe"
)

const (
	// The signing algorithm used for requests.
	signingAlgorithm = "AWS4-HMAC-SHA256"

	// A magic signature that tells S3 that the body is unsigned.
	unsignedPayload = "UNSIGNED-PAYLOAD"

	// The SHA256 hash for empty.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// AmzDateFormat is the format used by S3 for full dates.
	AmzDateFormat = "20060102T150405Z"

	// ShortDateFormat is the format used by S3 for short dates.
	ShortDateFormat = "20060102"
)

// Signer implements AWS Signature Version 4 for S3.
type Signer struct {
	accessKey string
	secretKey string
	region    string

	// Used for Request signing.
	secretKeyWithPrefix []byte

	// Since the date changes once a day, we cache it.
	// I did some benchmarks using RWMutex vs just hashing everytime, and using RWMutex (or even normal Mutex) is 100x
	// faster than hashing everytime, even with 10 goroutines and the contention they have.
	mu      sync.RWMutex
	keyDate string
	key     []byte
}

// NewSigner returns a new Signer for the specified keys and region.
func NewSigner(accessKey string, secretKey string, region string) *Signer {
	return &Signer{
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,

		secretKeyWithPrefix: []byte("AWS4" + secretKey),
	}
}

// AddSignatureHeaders adds the x-amz-date, x-amz-content-sha256 and Authorization headers to the provided request.
// payloadHash is the hex SHA-256 of the body, or "UNSIGNED-PAYLOAD".
func (s *Signer) AddSignatureHeaders(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(AmzDateFormat)
	shortDate := now.Format(ShortDateFormat)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders, canonHeaders := ToCanonicalHeaders(req.Header)
	canonicalRequest := strings.Join(
		[]string{
			req.Method,
			req.URL.EscapedPath(),
			req.URL.RawQuery,
			canonHeaders,
			signedHeaders,
			payloadHash,
		},
		"\n",
	)

	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := signingAlgorithm + "\n" + amzDate + "\n" + scope + "\n" + hashHex([]byte(canonicalRequest))

	signature := s.SignPayload(shortDate, stringToSign)
	req.Header.Set(
		"Authorization",
		signingAlgorithm+
			" Credential="+s.accessKey+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature,
	)
}

// SignPayload signs a payload and returns the hex signature derived from it.
// If the date is the same as the one passed last call, it will use a cached key.
// The date must be in ShortDateFormat format.
func (s *Signer) SignPayload(date string, payload string) string {
	var key []byte

	s.mu.RLock()
	if s.keyDate == date {
		key = s.key
		s.mu.RUnlock()

		goto haveKey
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keyDate != date {
		key = hmacSha256(s.secretKeyWithPrefix, date)
		key = hmacSha256(key, s.region)
		key = hmacSha256(key, "s3")
		key = hmacSha256(key, "aws4_request")

		s.key = key
		s.keyDate = date
	}

haveKey:

	return hex.EncodeToString(hmacSha256(key, payload))
}

// ShouldHeaderBeSigned returns whether the lowercase header name should be included in a SigV4 signature.
// It includes all mandatory headers and some extras that are commonly included in S3 requests.
func ShouldHeaderBeSigned(header string) bool {
	switch header {
	case "host", "content-md5", "content-type", "date", "range":
		return true
	}

	return strings.HasPrefix(header, "x-amz-")
}

// ToCanonicalHeaders encodes headers into their canonical forms.
// The first return is a semicolon-separated list of canonical header names that were encoded, and the second is the
// headers and their values in their canonical form as required by SigV4.
func ToCanonicalHeaders(headers http.Header) (signed string, canonical string) {
	valsCanon := make(map[string]string, len(headers))
	keysCanon := make([]string, 0, len(headers))

	for keyRaw, valsRaw := range headers {
		keyCanon := strings.ToLower(keyRaw)

		if !ShouldHeaderBeSigned(keyCanon) {
			continue
		}

		valsTrimmed := make([]string, len(valsRaw))
		for i, val := range valsRaw {
			valsTrimmed[i] = strings.Join(strings.Fields(val), " ")
		}

		valsCanon[keyCanon] = strings.Join(valsTrimmed, ",")
		keysCanon = append(keysCanon, keyCanon)
	}
	slices.Sort(keysCanon)

	var b strings.Builder
	for _, n := range keysCanon {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(valsCanon[n])
		b.WriteByte('\n')
	}

	return strings.Join(keysCanon, ";"), b.String()
}

// ToCanonicalQuery encodes query parameters with keys sorted and values URI-encoded as required by SigV4.
func ToCanonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))

	for key, vals := range q {
		uri := UriEncode(key, true)

		if len(vals) == 0 {
			pairs = append(pairs, uri+"=")
			continue
		}

		for _, val := range vals {
			pairs = append(pairs, uri+"="+UriEncode(val, true))
		}
	}

	slices.Sort(pairs)

	return strings.Join(pairs, "&")
}

// UriEncode implements the UriEncode function described in the AWS docs.
// See https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
func UriEncode(s string, encodeSlash bool) string {
	const hexDigits = "0123456789ABCDEF"

	var b strings.Builder
	b.Grow(len(s))

	for i := range len(s) {
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

func hmacSha256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)

	// This is safe because Write doesn't mutate its input and strings are immutable.
	// This avoids unnecessary copying caused by casting string to []byte.
	h.Write(unsafe.Slice(unsafe.StringData(data), len(data)))

	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
