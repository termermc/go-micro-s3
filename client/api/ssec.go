package api

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"net/http"
)

// SseCustomerKey is a 256-bit key for server-side encryption with customer-provided keys (SSE-C).
type SseCustomerKey [32]byte

// NewSseCustomerKey creates an SseCustomerKey from a 32-byte slice.
// If you already have a [32]byte, cast it to SseCustomerKey instead.
func NewSseCustomerKey(b []byte) (SseCustomerKey, error) {
	var k SseCustomerKey
	if len(b) != len(k) {
		return k, fmt.Errorf("s3: SSE-C key must be %d bytes, got %d", len(k), len(b))
	}
	copy(k[:], b)
	return k, nil
}

const (
	sseCPrefix     = "X-Amz-Server-Side-Encryption-Customer-"
	sseCCopyPrefix = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-"
)

// setSseC adds the SSE-C headers for k to h. A nil key is a no-op.
func setSseC(h http.Header, k *SseCustomerKey) {
	setSseCWithPrefix(h, sseCPrefix, k)
}

// setCopySourceSseC adds the copy-source SSE-C headers for k to h. A nil key
// is a no-op.
func setCopySourceSseC(h http.Header, k *SseCustomerKey) {
	setSseCWithPrefix(h, sseCCopyPrefix, k)
}

func setSseCWithPrefix(h http.Header, prefix string, k *SseCustomerKey) {
	if k == nil {
		return
	}
	sum := md5.Sum(k[:])
	h.Set(prefix+"Algorithm", "AES256")
	h.Set(prefix+"Key", base64.StdEncoding.EncodeToString(k[:]))
	h.Set(prefix+"Key-Md5", base64.StdEncoding.EncodeToString(sum[:]))
}
