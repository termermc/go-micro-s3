package api

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
)

// ErrNotFound is returned when a bucket, object or multipart upload are not found.
// You should use IsNotFound to check if an error qualifies as not found.
var ErrNotFound = errors.New("s3: not found")

// Error is an error response returned by the S3 service.
type Error struct {
	// Internal XML field for unmarshalling.
	XMLName xml.Name `xml:"Error"`

	StatusCode int    `xml:"-"`
	Code       string `xml:"Code"`
	Message    string `xml:"Message"`
	Resource   string `xml:"Resource"`
	RequestId  string `xml:"RequestId"`
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

// IsNotFound returns whether an error is a missing bucket, object or upload.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
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
