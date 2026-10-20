package client

import (
	"context"
	"errors"
	"iter"
	"time"

	"git.termer.net/termer/go-micro-s3/client/api"
)

// ObjectInfo describes an object.
type ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	ContentType  string
	LastModified time.Time

	// Metadata holds object custom metadata.
	// It will always be nil in results from Client.List.
	Metadata api.ObjectCustomMetadata
}

func objectInfoFromMeta(key string, m api.ObjectMeta) ObjectInfo {
	return ObjectInfo{
		Key:          key,
		Size:         m.ContentLength,
		ETag:         m.ETag,
		ContentType:  m.ContentType,
		LastModified: m.LastModified,
		Metadata:     m.Metadata,
	}
}

// StatOptions are optional parameters for Stat.
type StatOptions struct {
	// SseC is the key the object was encrypted with, if any.
	SseC *api.SseCustomerKey
}

// Stat returns an object's metadata.
// A missing object yields an error matching ErrNotFound. opts may be nil.
func (c *Client) Stat(ctx context.Context, bucket string, key string, opts *StatOptions) (ObjectInfo, error) {
	if opts == nil {
		opts = &StatOptions{}
	}
	out, err := c.api.HeadObject(ctx, &api.HeadObjectInput{Bucket: bucket, Key: key, SseC: opts.SseC})
	if err != nil {
		return ObjectInfo{}, err
	}
	return objectInfoFromMeta(key, out.ObjectMeta), nil
}

// Exists reports whether an object exists.
// For SSE-C objects, use Stat with the SSE-C key instead, since S3 rejects HEAD requests without it.
func (c *Client) Exists(ctx context.Context, bucket string, key string) (bool, error) {
	_, err := c.Stat(ctx, bucket, key, nil)
	if err != nil {
		if api.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// Delete deletes an object.
// Deleting an object that does not exist is a no-op.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	return c.api.DeleteObject(ctx, &api.DeleteObjectInput{Bucket: bucket, Key: key})
}

// DeleteMany deletes the specified keys in batches.
// Per-key failures are joined into the returned error.
func (c *Client) DeleteMany(ctx context.Context, bucket string, keys []string) error {
	var errs []error
	for len(keys) > 0 {
		n := min(len(keys), api.MaxDeleteObjects)

		out, err := c.api.DeleteObjects(ctx, &api.DeleteObjectsInput{
			Bucket: bucket,
			Keys:   keys[:n],
		})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}

		for _, e := range out.Errors {
			errs = append(errs, e)
		}

		keys = keys[n:]
	}

	return errors.Join(errs...)
}

// ObjectRef names an object.
type ObjectRef struct {
	Bucket string
	Key    string
}

// CopyOptions are optional parameters for Copy.
type CopyOptions struct {
	// SrcSseC is the key the source object is encrypted with, if any.
	SrcSseC *api.SseCustomerKey

	// SseC is the key to encrypt the destination object with, if any.
	SseC *api.SseCustomerKey

	// If true, the content type and user metadata will be replaced with ContentType and Metadata instead of copying
	// them from the source.
	ReplaceMetadata bool

	ContentType string
	Metadata    map[string]string
}

// Copy copies an object server-side (up to 5 GiB, the S3 single-copy limit).
// opts may be nil.
func (c *Client) Copy(ctx context.Context, src, dst ObjectRef, opts *CopyOptions) error {
	if opts == nil {
		opts = &CopyOptions{}
	}

	in := &api.CopyObjectInput{
		SrcBucket: src.Bucket,
		SrcKey:    src.Key,
		Bucket:    dst.Bucket,
		Key:       dst.Key,
		SrcSseC:   opts.SrcSseC,
		SseC:      opts.SseC,
	}
	if opts.ReplaceMetadata {
		in.MetadataDirective = api.MetadataDirectiveReplace
		in.ContentType = opts.ContentType
		in.Metadata = opts.Metadata
	}

	_, err := c.api.CopyObject(ctx, in)
	return err
}

// ListOptions are optional parameters for Client.List.
type ListOptions struct {
	Prefix string

	// Delimiter groups keys sharing a prefix up to the delimiter into a single Entry with IsPrefix set, e.g. "/" for
	// directory-style listing.
	Delimiter string

	StartAfter string

	// PageSize is the number of keys requested per page; 0 uses the server default.
	PageSize int
}

// Entry is an item yielded by Client.List.
// It is either an object or, when a delimiter is used, a common prefix (with only Key set).
type Entry struct {
	ObjectInfo
	IsPrefix bool
}

// List iterates over the objects in a bucket, fetching pages as needed.
// Iteration stops after the first error.
// opts may be nil.
func (c *Client) List(ctx context.Context, bucket string, opts *ListOptions) iter.Seq2[Entry, error] {
	if opts == nil {
		opts = &ListOptions{}
	}

	return func(yield func(Entry, error) bool) {
		in := &api.ListObjectsV2Input{
			Bucket:     bucket,
			Prefix:     opts.Prefix,
			Delimiter:  opts.Delimiter,
			StartAfter: opts.StartAfter,
			MaxKeys:    opts.PageSize,
		}

		for {
			out, err := c.api.ListObjectsV2(ctx, in)
			if err != nil {
				yield(Entry{}, err)
				return
			}

			for _, obj := range out.Contents {
				entry := Entry{
					Key:          obj.Key,
					Size:         obj.Size,
					ETag:         obj.ETag,
					LastModified: obj.LastModified,
				}
				if !yield(entry, nil) {
					return
				}
			}

			for _, p := range out.CommonPrefixes {
				entry := Entry{
					Key:      p,
					IsPrefix: true,
				}
				if !yield(entry, nil) {
					return
				}
			}

			if !out.IsTruncated || out.NextContinuationToken == "" {
				return
			}

			in.ContinuationToken = out.NextContinuationToken
		}
	}
}
