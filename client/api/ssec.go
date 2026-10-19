package api

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"net/http"
)

// SSECustomerKey is a 256-bit key for server-side encryption with
// customer-provided keys (SSE-C). The same key must be supplied to read an
// object that was written with it.
type SSECustomerKey [32]byte

// NewSSECustomerKey creates an SSECustomerKey from a 32-byte slice.
func NewSSECustomerKey(b []byte) (SSECustomerKey, error) {
	var k SSECustomerKey
	if len(b) != len(k) {
		return k, fmt.Errorf("s3: SSE-C key must be %d bytes, got %d", len(k), len(b))
	}
	copy(k[:], b)
	return k, nil
}

const (
	ssecPrefix     = "X-Amz-Server-Side-Encryption-Customer-"
	ssecCopyPrefix = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-"
)

// setSSEC adds the SSE-C headers for k to h. A nil key is a no-op.
func setSSEC(h http.Header, k *SSECustomerKey) {
	setSSECWithPrefix(h, ssecPrefix, k)
}

// setCopySourceSSEC adds the copy-source SSE-C headers for k to h. A nil key
// is a no-op.
func setCopySourceSSEC(h http.Header, k *SSECustomerKey) {
	setSSECWithPrefix(h, ssecCopyPrefix, k)
}

func setSSECWithPrefix(h http.Header, prefix string, k *SSECustomerKey) {
	if k == nil {
		return
	}
	sum := md5.Sum(k[:])
	h.Set(prefix+"Algorithm", "AES256")
	h.Set(prefix+"Key", base64.StdEncoding.EncodeToString(k[:]))
	h.Set(prefix+"Key-Md5", base64.StdEncoding.EncodeToString(sum[:]))
}
