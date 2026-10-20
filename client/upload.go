package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// UploadOptions are optional parameters for Client.Upload.
type UploadOptions struct {
	ContentType string
	Metadata    api.ObjectCustomMetadata

	// SseC is the SSE-C key to use for encrypting the object, if any.
	SseC *api.SseCustomerKey

	// ExtraHeaders are additional headers to send with every request the call makes, if any.
	// Headers set by the call itself take precedence over extra headers with the same name.
	ExtraHeaders http.Header
}

// UploadResult is the result of Client.Upload.
type UploadResult struct {
	ETag string
}

// TODO Make this an option
// abortTimeout bounds the cleanup request sent after a failed multipart upload.
const abortTimeout = time.Minute

// Upload uploads an object.
// Objects larger than the multipart threshold are uploaded in parts.
// If you already have the content you want to upload in memory, use Client.UploadBytes instead.
//
// If content implements io.ReaderAt (e.g. os.File, bytes.Reader), parts are read with io.SectionReader and uploaded
// concurrently, starting at content's current offset if it also implements io.Seeker (content is then left positioned
// after the uploaded data). Otherwise, parts are read from content sequentially.
//
// content must yield AT LEAST contentLen bytes; a short read fails the upload, and a
// failed multipart upload is aborted.
//
// opts may be nil.
func (c *Client) Upload(
	ctx context.Context,
	bucket string,
	key string,
	content io.Reader,
	contentLen int64,
	opts *UploadOptions,
) (UploadResult, error) {
	if contentLen < 0 {
		return UploadResult{}, errors.New("s3: upload size must not be negative")
	}
	if content == nil && contentLen > 0 {
		return UploadResult{}, errors.New("s3: nil reader with non-zero size")
	}
	if opts == nil {
		opts = &UploadOptions{}
	}

	if contentLen <= c.multipartThreshold {
		out, err := c.api.PutObject(ctx, &api.PutObjectInput{
			Bucket:        bucket,
			Key:           key,
			Body:          content,
			ContentLength: contentLen,
			ContentType:   opts.ContentType,
			Metadata:      opts.Metadata,
			SseC:          opts.SseC,
			ExtraHeaders:  opts.ExtraHeaders,
		})
		if err != nil {
			return UploadResult{}, err
		}
		return UploadResult{
			ETag: out.ETag,
		}, nil
	}

	return c.uploadMultipart(ctx, bucket, key, content, contentLen, opts)
}

// UploadBytes uploads an object from a byte slice.
// Objects larger than the multipart threshold are uploaded in parts.
// If your content comes from an io.Reader, use Client.Upload instead.
//
// opts may be nil.
func (c *Client) UploadBytes(
	ctx context.Context,
	bucket string,
	key string,
	content []byte,
	opts *UploadOptions,
) (UploadResult, error) {
	_ = bytes.Reader{}
	return c.Upload(ctx, bucket, key, bytes.NewReader(content), int64(len(content)), opts)
}

// partLayout returns the part size and count for an object of the specified size.
func (c *Client) partLayout(size int64) (partSize int64, numParts int, err error) {
	partSize = c.partSize
	if size > partSize*api.MaxParts {
		partSize = (size + api.MaxParts - 1) / api.MaxParts
	}
	if partSize > api.MaxPartSize {
		return 0, 0, fmt.Errorf("s3: object of %d bytes exceeds the multipart size limit", size)
	}

	numParts = int((size + partSize - 1) / partSize)

	return partSize, max(numParts, 1), nil
}

// asReaderAt reports whether r can be read concurrently at arbitrary offsets, returning it as an io.ReaderAt along with
// the offset to start reading at.
// Files that cannot seek, such as pipes, are read sequentially.
func (c *Client) asReaderAt(r io.Reader) (io.ReaderAt, int64, bool) {
	if c.concurrency <= 1 {
		return nil, 0, false
	}

	ra, is := r.(io.ReaderAt)
	if !is {
		return nil, 0, false
	}

	var base int64
	if seeker, is := r.(io.Seeker); is {
		off, err := seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, false
		}

		base = off
	}

	return ra, base, true
}

func (c *Client) uploadMultipart(ctx context.Context, bucket, key string, content io.Reader, size int64, opts *UploadOptions) (UploadResult, error) {
	partSize, numParts, err := c.partLayout(size)
	if err != nil {
		return UploadResult{}, err
	}

	created, err := c.api.CreateMultipartUpload(ctx, &api.CreateMultipartUploadInput{
		Bucket:       bucket,
		Key:          key,
		ContentType:  opts.ContentType,
		Metadata:     opts.Metadata,
		SseC:         opts.SseC,
		ExtraHeaders: opts.ExtraHeaders,
	})
	if err != nil {
		return UploadResult{}, err
	}

	uploadId := created.UploadId

	uploadPart := func(ctx context.Context, i int, body io.Reader) (api.CompletedPart, error) {
		n := min(partSize, size-int64(i)*partSize)

		out, err := c.api.UploadPart(ctx, &api.UploadPartInput{
			Bucket:        bucket,
			Key:           key,
			UploadId:      uploadId,
			PartNumber:    i + 1,
			Body:          body,
			ContentLength: n,
			SseC:          opts.SseC,
			ExtraHeaders:  opts.ExtraHeaders,
		})
		if err != nil {
			return api.CompletedPart{}, fmt.Errorf("s3: uploading part %d: %w", i+1, err)
		}

		return api.CompletedPart{PartNumber: i + 1, ETag: out.ETag}, nil
	}

	// Upload concurrently if possible, otherwise do it in sequence.
	// We can only upload concurrently if content implements io.ReaderAt.
	var parts []api.CompletedPart
	ra, base, parallel := c.asReaderAt(content)
	if parallel && numParts > 1 {
		parts, err = c.uploadPartsParallel(
			ctx,
			numParts,
			func(ctx context.Context, i int) (api.CompletedPart, error) {
				off := int64(i) * partSize
				n := min(partSize, size-off)
				return uploadPart(ctx, i, io.NewSectionReader(ra, base+off, n))
			},
		)
		if err == nil {
			if s, ok := content.(io.Seeker); ok {
				_, err = s.Seek(base+size, io.SeekStart)
			}
		}
	} else {
		parts = make([]api.CompletedPart, numParts)
		for i := range numParts {
			n := min(partSize, size-int64(i)*partSize)

			var p api.CompletedPart
			p, err = uploadPart(ctx, i, io.LimitReader(content, n))
			if err != nil {
				break
			}

			parts[i] = p
		}
	}

	if err == nil {
		var out *api.CompleteMultipartUploadOutput
		out, err = c.api.CompleteMultipartUpload(ctx, &api.CompleteMultipartUploadInput{
			Bucket:       bucket,
			Key:          key,
			UploadId:     uploadId,
			Parts:        parts,
			ExtraHeaders: opts.ExtraHeaders,
		})
		if err == nil {
			return UploadResult{ETag: out.ETag}, nil
		}
	}

	// Common error handling code for part upload failure and completion failure.
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	abortErr := c.api.AbortMultipartUpload(abortCtx, &api.AbortMultipartUploadInput{
		Bucket:       bucket,
		Key:          key,
		UploadId:     uploadId,
		ExtraHeaders: opts.ExtraHeaders,
	})
	if abortErr != nil {
		return UploadResult{}, errors.Join(err, fmt.Errorf("s3: aborting multipart upload %s: %w", uploadId, abortErr))
	}

	// Success!
	return UploadResult{}, err
}

// uploadPartsParallel runs upload for parts [0, numParts) on up to c.concurrency workers, stopping at the first error.
func (c *Client) uploadPartsParallel(
	ctx context.Context,
	numParts int,
	upload func(context.Context, int) (api.CompletedPart, error),
) ([]api.CompletedPart, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	parts := make([]api.CompletedPart, numParts)
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range min(c.concurrency, numParts) {
		wg.Go(func() {
			for i := range jobs {
				p, err := upload(ctx, i)
				if err != nil {
					cancel(err)
					continue
				}
				parts[i] = p
			}
		})
	}

feed:
	for i := range numParts {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}

	return parts, nil
}
