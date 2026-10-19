package api

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

const s3XMLNS = "http://s3.amazonaws.com/doc/2006-03-01/"

// maxXMLBody bounds how much of an XML response is read.
const maxXMLBody = 16 << 20

// decodeXML reads resp's body into v and closes it.
func decodeXML(resp *http.Response, v any) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXMLBody))
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(body, v); err != nil {
		return fmt.Errorf("s3: decoding response: %w", err)
	}
	return nil
}

// readBody reads resp's body and closes it.
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxXMLBody))
}

// Owner identifies the owner of a bucket or object.
type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type listAllMyBucketsResult struct {
	Owner   Owner        `xml:"Owner"`
	Buckets []BucketInfo `xml:"Buckets>Bucket"`
}

// BucketInfo describes a bucket in a ListBuckets response.
type BucketInfo struct {
	Name         string    `xml:"Name"`
	CreationDate time.Time `xml:"CreationDate"`
}

type createBucketConfiguration struct {
	XMLName            xml.Name `xml:"CreateBucketConfiguration"`
	XMLNS              string   `xml:"xmlns,attr"`
	LocationConstraint string   `xml:"LocationConstraint"`
}

// Object describes an object in a ListObjectsV2 response.
type Object struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
}

type commonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResult struct {
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	KeyCount              int            `xml:"KeyCount"`
	MaxKeys               int            `xml:"MaxKeys"`
	IsTruncated           bool           `xml:"IsTruncated"`
	Contents              []Object       `xml:"Contents"`
	CommonPrefixes        []commonPrefix `xml:"CommonPrefixes"`
	NextContinuationToken string         `xml:"NextContinuationToken"`
	StartAfter            string         `xml:"StartAfter"`
}

type deleteRequest struct {
	XMLName xml.Name           `xml:"Delete"`
	XMLNS   string             `xml:"xmlns,attr"`
	Objects []deleteIdentifier `xml:"Object"`
	Quiet   bool               `xml:"Quiet"`
}

type deleteIdentifier struct {
	Key string `xml:"Key"`
}

type deleteResult struct {
	Deleted []deleteIdentifier `xml:"Deleted"`
	Errors  []DeleteError      `xml:"Error"`
}

// DeleteError describes a key that could not be deleted by DeleteObjects.
type DeleteError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (e DeleteError) Error() string {
	return fmt.Sprintf("s3: deleting %q: %s: %s", e.Key, e.Code, e.Message)
}

type copyObjectResult struct {
	ETag         string    `xml:"ETag"`
	LastModified time.Time `xml:"LastModified"`
}

type initiateMultipartUploadResult struct {
	Bucket   string `xml:"Bucket"`
	Key      string `xml:"Key"`
	UploadID string `xml:"UploadId"`
}

// CompletedPart identifies an uploaded part for CompleteMultipartUpload.
type CompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartUpload struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	XMLNS   string          `xml:"xmlns,attr"`
	Parts   []CompletedPart `xml:"Part"`
}

type completeMultipartUploadResult struct {
	Location string `xml:"Location"`
	Bucket   string `xml:"Bucket"`
	Key      string `xml:"Key"`
	ETag     string `xml:"ETag"`
}

// MultipartUpload describes an in-progress multipart upload.
type MultipartUpload struct {
	Key       string    `xml:"Key"`
	UploadID  string    `xml:"UploadId"`
	Initiated time.Time `xml:"Initiated"`
}

type listMultipartUploadsResult struct {
	Bucket             string            `xml:"Bucket"`
	KeyMarker          string            `xml:"KeyMarker"`
	UploadIDMarker     string            `xml:"UploadIdMarker"`
	NextKeyMarker      string            `xml:"NextKeyMarker"`
	NextUploadIDMarker string            `xml:"NextUploadIdMarker"`
	IsTruncated        bool              `xml:"IsTruncated"`
	Uploads            []MultipartUpload `xml:"Upload"`
	CommonPrefixes     []commonPrefix    `xml:"CommonPrefixes"`
}
