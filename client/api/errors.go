package api

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrNotFound matches (via errors.Is) any error caused by a missing bucket,
// object or multipart upload.
var ErrNotFound = errors.New("s3: not found")

// Error is an error response returned by the S3 service.
type Error struct {
	StatusCode int    `xml:"-"`
	Code       string `xml:"Code"`
	Message    string `xml:"Message"`
	Resource   string `xml:"Resource"`
	RequestID  string `xml:"RequestId"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("s3: %s (%d): %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("s3: %s (%d)", e.Code, e.StatusCode)
}

// Is makes errors.Is(err, ErrNotFound) report true for not-found errors.
func (e *Error) Is(target error) bool {
	if target != ErrNotFound {
		return false
	}
	switch e.Code {
	case "NoSuchKey", "NoSuchBucket", "NoSuchUpload", "NotFound":
		return true
	}
	return e.StatusCode == http.StatusNotFound
}

// IsNotFound reports whether err indicates a missing bucket, object or upload.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// maxErrorBody bounds how much of an error response is read.
const maxErrorBody = 64 << 10

// parseError builds an *Error from a non-2xx response. It does not close the
// body.
func parseError(resp *http.Response) error {
	e := &Error{StatusCode: resp.StatusCode}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if len(body) > 0 {
		_ = xml.Unmarshal(body, e)
	}
	if e.Code == "" {
		e.Code = codeForStatus(resp.StatusCode)
	}
	if e.RequestID == "" {
		e.RequestID = resp.Header.Get("X-Amz-Request-Id")
	}
	return e
}

// parseEmbeddedError detects the S3 quirk where a 200 response to
// CopyObject/CompleteMultipartUpload carries an <Error> document.
func parseEmbeddedError(statusCode int, body []byte) error {
	var probe struct{ XMLName xml.Name }
	if xml.Unmarshal(body, &probe) != nil || probe.XMLName.Local != "Error" {
		return nil
	}
	e := &Error{StatusCode: statusCode}
	_ = xml.Unmarshal(body, e)
	return e
}

// codeForStatus provides an error code for bodiless (e.g. HEAD) responses.
func codeForStatus(status int) string {
	switch status {
	case http.StatusNotFound:
		return "NotFound"
	case http.StatusForbidden:
		return "Forbidden"
	case http.StatusBadRequest:
		return "BadRequest"
	case http.StatusConflict:
		return "Conflict"
	case http.StatusPreconditionFailed:
		return "PreconditionFailed"
	case http.StatusNotModified:
		return "NotModified"
	}
	return http.StatusText(status)
}
