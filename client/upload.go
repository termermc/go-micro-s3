package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// UploadOptions are optional parameters for Upload.
type UploadOptions struct {
	ContentType string
	Metadata    map[string]string
	// SSEC encrypts the object with a customer-provided key.
	SSEC *SSECustomerKey
}

// UploadResult is the result of Upload.
type UploadResult struct {
	ETag string
}

// abortTimeout bounds the cleanup request sent after a failed multipart upload.
const abortTimeout = time.Minute

// Upload streams size bytes from r to bucket/key. Nothing is buffered: the
// data is read from r as it is sent.
//
// Objects larger than the multipart threshold are uploaded in parts. If r
// implements io.ReaderAt (e.g. *os.File, *bytes.Reader), parts are read with
// io.SectionReader and uploaded concurrently, starting at r's current offset
// if it also implements io.Seeker (r is then left positioned after the
// uploaded data). Otherwise parts are read from r sequentially.
//
// r must yield at least size bytes; a short read fails the upload, and a
// failed multipart upload is aborted. opts may be nil.
func (c *Client) Upload(ctx context.Context, bucket, key string, r io.Reader, size int64, opts *UploadOptions) (UploadResult, error) {
	if size < 0 {
		return UploadResult{}, errors.New("s3: upload size must not be negative")
	}
	if r == nil && size > 0 {
		return UploadResult{}, errors.New("s3: nil reader with non-zero size")
	}
	if opts == nil {
		opts = &UploadOptions{}
	}
	if size <= c.multipartThreshold {
		out, err := c.api.PutObject(ctx, &api.PutObjectInput{
			Bucket:        bucket,
			Key:           key,
			Body:          r,
			ContentLength: size,
			ContentType:   opts.ContentType,
			Metadata:      opts.Metadata,
			SseC:          opts.SSEC,
		})
		if err != nil {
			return UploadResult{}, err
		}
		return UploadResult{ETag: out.ETag}, nil
	}
	return c.uploadMultipart(ctx, bucket, key, r, size, opts)
}

// partLayout returns the part size and count for an object of the given size.
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

func (c *Client) uploadMultipart(ctx context.Context, bucket, key string, r io.Reader, size int64, opts *UploadOptions) (UploadResult, error) {
	partSize, numParts, err := c.partLayout(size)
	if err != nil {
		return UploadResult{}, err
	}

	created, err := c.api.CreateMultipartUpload(ctx, &api.CreateMultipartUploadInput{
		Bucket:      bucket,
		Key:         key,
		ContentType: opts.ContentType,
		Metadata:    opts.Metadata,
		SseC:        opts.SSEC,
	})
	if err != nil {
		return UploadResult{}, err
	}
	uploadID := created.UploadID

	uploadPart := func(ctx context.Context, i int, body io.Reader) (api.CompletedPart, error) {
		n := min(partSize, size-int64(i)*partSize)
		out, err := c.api.UploadPart(ctx, &api.UploadPartInput{
			Bucket:        bucket,
			Key:           key,
			UploadID:      uploadID,
			PartNumber:    i + 1,
			Body:          body,
			ContentLength: n,
			SseC:          opts.SSEC,
		})
		if err != nil {
			return api.CompletedPart{}, fmt.Errorf("s3: uploading part %d: %w", i+1, err)
		}
		return api.CompletedPart{PartNumber: i + 1, ETag: out.ETag}, nil
	}

	var parts []api.CompletedPart
	ra, base, parallel := c.readerAt(r)
	if parallel && numParts > 1 {
		parts, err = c.uploadPartsParallel(ctx, numParts, func(ctx context.Context, i int) (api.CompletedPart, error) {
			off := int64(i) * partSize
			n := min(partSize, size-off)
			return uploadPart(ctx, i, io.NewSectionReader(ra, base+off, n))
		})
		if err == nil {
			if s, ok := r.(io.Seeker); ok {
				_, err = s.Seek(base+size, io.SeekStart)
			}
		}
	} else {
		parts = make([]api.CompletedPart, 0, numParts)
		for i := range numParts {
			n := min(partSize, size-int64(i)*partSize)
			var p api.CompletedPart
			p, err = uploadPart(ctx, i, io.LimitReader(r, n))
			if err != nil {
				break
			}
			parts = append(parts, p)
		}
	}

	if err == nil {
		var out *api.CompleteMultipartUploadOutput
		out, err = c.api.CompleteMultipartUpload(ctx, &api.CompleteMultipartUploadInput{
			Bucket:   bucket,
			Key:      key,
			UploadID: uploadID,
			Parts:    parts,
		})
		if err == nil {
			return UploadResult{ETag: out.ETag}, nil
		}
	}

	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	abortErr := c.api.AbortMultipartUpload(abortCtx, &api.AbortMultipartUploadInput{
		Bucket:   bucket,
		Key:      key,
		UploadID: uploadID,
	})
	if abortErr != nil {
		return UploadResult{}, errors.Join(err, fmt.Errorf("s3: aborting multipart upload %s: %w", uploadID, abortErr))
	}
	return UploadResult{}, err
}

// readerAt reports whether r can be read concurrently at arbitrary offsets,
// returning it as an io.ReaderAt along with the offset to start reading at.
// Files that cannot seek (e.g. pipes) are read sequentially.
func (c *Client) readerAt(r io.Reader) (io.ReaderAt, int64, bool) {
	if c.concurrency <= 1 {
		return nil, 0, false
	}
	ra, ok := r.(io.ReaderAt)
	if !ok {
		return nil, 0, false
	}
	var base int64
	if s, ok := r.(io.Seeker); ok {
		off, err := s.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, false
		}
		base = off
	}
	return ra, base, true
}

// uploadPartsParallel runs upload for parts [0, numParts) on up to
// c.concurrency workers, stopping at the first error.
func (c *Client) uploadPartsParallel(ctx context.Context, numParts int, upload func(context.Context, int) (api.CompletedPart, error)) ([]api.CompletedPart, error) {
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
