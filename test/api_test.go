package test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"git.termer.net/termer/go-micro-s3/client/api"
)

func putString(t *testing.T, c *api.Client, bucket, key, data string) {
	t.Helper()
	_, err := c.PutObject(context.Background(), &api.PutObjectInput{
		Bucket:        bucket,
		Key:           key,
		Body:          strings.NewReader(data),
		ContentLength: int64(len(data)),
	})
	if err != nil {
		t.Fatalf("PutObject(%q): %v", key, err)
	}
}

func getString(t *testing.T, c *api.Client, in *api.GetObjectInput) string {
	t.Helper()
	out, err := c.GetObject(context.Background(), in)
	if err != nil {
		t.Fatalf("GetObject(%q): %v", in.Key, err)
	}
	return string(readAll(t, out.Body))
}

// wantS3Error asserts that err is an *api.Error with one of the given statuses.
// It will return the error as an *api.Error if it was one, otherwise it calls t.Fatalf.
func wantS3Error(t *testing.T, err error, statuses ...int) *api.Error {
	t.Helper()
	s3Err, is := errors.AsType[*api.Error](err)
	if !is {
		t.Fatalf("expected *api.Error, got %v", err)
	}
	if !slices.Contains(statuses, s3Err.StatusCode) {
		t.Fatalf("expected status in %v, got %v", statuses, s3Err)
	}
	return s3Err
}

func TestAPIBuckets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	name := bucketName()

	if err := c.CreateBucket(ctx, &api.CreateBucketInput{Bucket: name}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := c.HeadBucket(ctx, &api.HeadBucketInput{Bucket: name}); err != nil {
		t.Fatalf("HeadBucket: %v", err)
	}

	list, err := c.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if !slices.ContainsFunc(list.Buckets, func(b api.BucketInfo) bool { return b.Name == name }) {
		t.Errorf("ListBuckets does not contain %q: %+v", name, list.Buckets)
	}

	if err := c.DeleteBucket(ctx, &api.DeleteBucketInput{Bucket: name}); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	err = c.HeadBucket(ctx, &api.HeadBucketInput{Bucket: name})
	if !api.IsNotFound(err) {
		t.Fatalf("HeadBucket after delete: expected not found, got %v", err)
	}
}

func TestAPIObjectRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	key := prefix(t) + "hello.txt"
	data := "Hello, Garage!"

	put, err := c.PutObject(ctx, &api.PutObjectInput{
		Bucket:        env.Bucket,
		Key:           key,
		Body:          strings.NewReader(data),
		ContentLength: int64(len(data)),
		ContentType:   "text/plain",
		Metadata:      map[string]string{"Color": "blue", "x-thing": "1"},
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if put.ETag == "" {
		t.Error("PutObject returned empty ETag")
	}

	head, err := c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: key})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if head.ContentLength != int64(len(data)) || head.ContentType != "text/plain" || head.ETag != put.ETag {
		t.Errorf("HeadObject = %+v", head.ObjectMeta)
	}
	if head.Metadata["color"] != "blue" || head.Metadata["x-thing"] != "1" {
		t.Errorf("HeadObject metadata = %v", head.Metadata)
	}
	if head.LastModified.IsZero() {
		t.Error("HeadObject LastModified is zero")
	}

	get, err := c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if got := string(readAll(t, get.Body)); got != data {
		t.Errorf("GetObject body = %q, want %q", got, data)
	}
	if get.Metadata["color"] != "blue" {
		t.Errorf("GetObject metadata = %v", get.Metadata)
	}

	rng, err := c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key, Range: "bytes=7-12"})
	if err != nil {
		t.Fatalf("GetObject range: %v", err)
	}
	if got := string(readAll(t, rng.Body)); got != "Garage" {
		t.Errorf("ranged body = %q", got)
	}
	if rng.ContentRange != "bytes 7-12/14" {
		t.Errorf("Content-Range = %q", rng.ContentRange)
	}

	if err := c.DeleteObject(ctx, &api.DeleteObjectInput{Bucket: env.Bucket, Key: key}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	_, err = c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: key})
	if !api.IsNotFound(err) {
		t.Fatalf("HeadObject after delete: expected not found, got %v", err)
	}
	_, err = c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key})
	if e := wantS3Error(t, err, 404); e.Code != "NoSuchKey" {
		t.Errorf("GetObject after delete code = %q", e.Code)
	}
	// Deleting a missing key succeeds.
	if err := c.DeleteObject(ctx, &api.DeleteObjectInput{Bucket: env.Bucket, Key: key}); err != nil {
		t.Errorf("DeleteObject on missing key: %v", err)
	}
}

func TestAPIEmptyObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	key := prefix(t) + "empty"

	if _, err := c.PutObject(ctx, &api.PutObjectInput{Bucket: env.Bucket, Key: key}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: key}); got != "" {
		t.Errorf("body = %q", got)
	}
}

func TestAPISpecialKeys(t *testing.T) {
	t.Parallel()
	c := newAPI(t)
	p := prefix(t)
	keys := []string{
		"with space.txt",
		"plus+sign",
		"percent%20literal",
		"unicode-日本語-é",
		"double//slash",
		"tilde~and=equals&amp",
		"semi;colon,comma:colon@at!bang'quote(paren)*star$dollar",
		"q?mark#hash",
	}
	for _, k := range keys {
		key := p + k
		putString(t, c, env.Bucket, key, k)
		if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: key}); got != k {
			t.Errorf("key %q: body = %q", key, got)
		}
	}

	out, err := c.ListObjectsV2(context.Background(), &api.ListObjectsV2Input{Bucket: env.Bucket, Prefix: p})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	var listed []string
	for _, o := range out.Contents {
		listed = append(listed, strings.TrimPrefix(o.Key, p))
	}
	want := slices.Clone(keys)
	slices.Sort(want)
	if !slices.Equal(listed, want) {
		t.Errorf("listed keys = %q, want %q", listed, want)
	}
}

func TestAPIListObjectsV2(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	p := prefix(t)
	keys := []string{"a", "b", "c", "dir1/x", "dir1/y", "dir2/z", "e"}
	for _, k := range keys {
		putString(t, c, env.Bucket, p+k, k)
	}

	// Paginate with a small page size.
	var got []string
	var token string
	pages := 0
	for {
		out, err := c.ListObjectsV2(ctx, &api.ListObjectsV2Input{
			Bucket:            env.Bucket,
			Prefix:            p,
			MaxKeys:           2,
			ContinuationToken: token,
		})
		if err != nil {
			t.Fatalf("ListObjectsV2: %v", err)
		}
		pages++
		if len(out.Contents) > 2 {
			t.Fatalf("page has %d keys, want <= 2", len(out.Contents))
		}
		for _, o := range out.Contents {
			got = append(got, strings.TrimPrefix(o.Key, p))
			if o.Size != int64(len(strings.TrimPrefix(o.Key, p))) || o.ETag == "" {
				t.Errorf("object %+v", o)
			}
		}
		if !out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	if !slices.Equal(got, keys) {
		t.Errorf("paginated keys = %q, want %q", got, keys)
	}
	if pages != 4 {
		t.Errorf("pages = %d, want 4", pages)
	}

	// Delimiter groups directories into common prefixes.
	out, err := c.ListObjectsV2(ctx, &api.ListObjectsV2Input{Bucket: env.Bucket, Prefix: p, Delimiter: "/"})
	if err != nil {
		t.Fatalf("ListObjectsV2 delimiter: %v", err)
	}
	var objs []string
	for _, o := range out.Contents {
		objs = append(objs, strings.TrimPrefix(o.Key, p))
	}
	if want := []string{"a", "b", "c", "e"}; !slices.Equal(objs, want) {
		t.Errorf("delimiter objects = %q, want %q", objs, want)
	}
	if want := []string{p + "dir1/", p + "dir2/"}; !slices.Equal(out.CommonPrefixes, want) {
		t.Errorf("common prefixes = %q, want %q", out.CommonPrefixes, want)
	}

	// StartAfter skips keys.
	out, err = c.ListObjectsV2(ctx, &api.ListObjectsV2Input{Bucket: env.Bucket, Prefix: p, StartAfter: p + "dir1/y"})
	if err != nil {
		t.Fatalf("ListObjectsV2 start-after: %v", err)
	}
	objs = nil
	for _, o := range out.Contents {
		objs = append(objs, strings.TrimPrefix(o.Key, p))
	}
	if want := []string{"dir2/z", "e"}; !slices.Equal(objs, want) {
		t.Errorf("start-after objects = %q, want %q", objs, want)
	}
}

func TestAPICopyObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	p := prefix(t)
	data := "copy me"

	_, err := c.PutObject(ctx, &api.PutObjectInput{
		Bucket:        env.Bucket,
		Key:           p + "src file",
		Body:          strings.NewReader(data),
		ContentLength: int64(len(data)),
		ContentType:   "text/plain",
		Metadata:      map[string]string{"orig": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// COPY directive keeps metadata.
	out, err := c.CopyObject(ctx, &api.CopyObjectInput{
		SrcBucket: env.Bucket, SrcKey: p + "src file",
		Bucket: env.Bucket, Key: p + "dst-copy",
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if out.ETag == "" {
		t.Error("CopyObject returned empty ETag")
	}
	head, err := c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: p + "dst-copy"})
	if err != nil {
		t.Fatal(err)
	}
	if head.Metadata["orig"] != "yes" || head.ContentType != "text/plain" {
		t.Errorf("copied meta = %+v", head.ObjectMeta)
	}
	if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: p + "dst-copy"}); got != data {
		t.Errorf("copied body = %q", got)
	}

	// REPLACE directive replaces metadata.
	_, err = c.CopyObject(ctx, &api.CopyObjectInput{
		SrcBucket: env.Bucket, SrcKey: p + "src file",
		Bucket: env.Bucket, Key: p + "dst-replace",
		MetadataDirective: api.MetadataDirectiveReplace,
		ContentType:       "application/x-new",
		Metadata:          map[string]string{"new": "1"},
	})
	if err != nil {
		t.Fatalf("CopyObject replace: %v", err)
	}
	head, err = c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: p + "dst-replace"})
	if err != nil {
		t.Fatal(err)
	}
	if head.Metadata["new"] != "1" || head.Metadata["orig"] != "" || head.ContentType != "application/x-new" {
		t.Errorf("replaced meta = %+v", head.ObjectMeta)
	}

	// Copying a missing source fails.
	_, err = c.CopyObject(ctx, &api.CopyObjectInput{
		SrcBucket: env.Bucket, SrcKey: p + "missing",
		Bucket: env.Bucket, Key: p + "dst-missing",
	})
	if !api.IsNotFound(err) {
		t.Errorf("CopyObject missing source: expected not found, got %v", err)
	}
}

func TestAPIDeleteObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	p := prefix(t)
	var keys []string
	for _, k := range []string{"1", "2", "3", "with space", "x+y"} {
		putString(t, c, env.Bucket, p+k, k)
		keys = append(keys, p+k)
	}

	out, err := c.DeleteObjects(ctx, &api.DeleteObjectsInput{Bucket: env.Bucket, Keys: keys})
	if err != nil {
		t.Fatalf("DeleteObjects: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Errorf("DeleteObjects errors: %v", out.Errors)
	}
	slices.Sort(out.Deleted)
	if !slices.Equal(out.Deleted, keys) {
		t.Errorf("deleted = %q, want %q", out.Deleted, keys)
	}

	list, err := c.ListObjectsV2(ctx, &api.ListObjectsV2Input{Bucket: env.Bucket, Prefix: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Contents) != 0 {
		t.Errorf("objects remain after DeleteObjects: %+v", list.Contents)
	}
}

func TestAPIMultipart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	key := prefix(t) + "multi"
	sizes := []int64{api.MinPartSize, api.MinPartSize, 1234}

	created, err := c.CreateMultipartUpload(ctx, &api.CreateMultipartUploadInput{
		Bucket: env.Bucket, Key: key, ContentType: "application/x-test",
		Metadata: map[string]string{"parts": "3"},
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	var parts []api.CompletedPart
	var all []byte
	for i, n := range sizes {
		data := genBytes(byte(i), n)
		all = append(all, data...)
		out, err := c.UploadPart(ctx, &api.UploadPartInput{
			Bucket: env.Bucket, Key: key, UploadID: created.UploadID,
			PartNumber: i + 1, Body: bytes.NewReader(data), ContentLength: n,
		})
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		parts = append(parts, api.CompletedPart{PartNumber: i + 1, ETag: out.ETag})
	}

	uploads, err := c.ListMultipartUploads(ctx, &api.ListMultipartUploadsInput{Bucket: env.Bucket, Prefix: key})
	if err != nil {
		t.Fatalf("ListMultipartUploads: %v", err)
	}
	if len(uploads.Uploads) != 1 || uploads.Uploads[0].UploadID != created.UploadID {
		t.Errorf("in-progress uploads = %+v", uploads.Uploads)
	}

	done, err := c.CompleteMultipartUpload(ctx, &api.CompleteMultipartUploadInput{
		Bucket: env.Bucket, Key: key, UploadID: created.UploadID, Parts: parts,
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if done.ETag == "" {
		t.Error("CompleteMultipartUpload returned empty ETag")
	}

	get, err := c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, get.Body); !bytes.Equal(got, all) {
		t.Errorf("multipart body mismatch: got %d bytes, want %d", len(got), len(all))
	}
	if get.ContentType != "application/x-test" || get.Metadata["parts"] != "3" {
		t.Errorf("multipart meta = %+v", get.ObjectMeta)
	}
}

func TestAPIMultipartAbort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	key := prefix(t) + "aborted"

	created, err := c.CreateMultipartUpload(ctx, &api.CreateMultipartUploadInput{Bucket: env.Bucket, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UploadPart(ctx, &api.UploadPartInput{
		Bucket: env.Bucket, Key: key, UploadID: created.UploadID,
		PartNumber: 1, Body: strings.NewReader("part"), ContentLength: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = c.AbortMultipartUpload(ctx, &api.AbortMultipartUploadInput{Bucket: env.Bucket, Key: key, UploadID: created.UploadID})
	if err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}

	uploads, err := c.ListMultipartUploads(ctx, &api.ListMultipartUploadsInput{Bucket: env.Bucket, Prefix: key})
	if err != nil {
		t.Fatal(err)
	}
	if len(uploads.Uploads) != 0 {
		t.Errorf("uploads remain after abort: %+v", uploads.Uploads)
	}
	_, err = c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: key})
	if !api.IsNotFound(err) {
		t.Errorf("aborted object exists: %v", err)
	}
}

func TestAPIShortBody(t *testing.T) {
	t.Parallel()
	c := newAPI(t)
	key := prefix(t) + "short"
	_, err := c.PutObject(context.Background(), &api.PutObjectInput{
		Bucket: env.Bucket, Key: key, Body: strings.NewReader("abc"), ContentLength: 10,
	})
	if err == nil {
		t.Fatal("PutObject with short body succeeded")
	}
}

func TestAPISSEC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	p := prefix(t)
	key := p + "secret"
	data := "top secret data"
	k1, k2 := randomKey(t), randomKey(t)

	_, err := c.PutObject(ctx, &api.PutObjectInput{
		Bucket: env.Bucket, Key: key, Body: strings.NewReader(data),
		ContentLength: int64(len(data)), SSEC: &k1,
	})
	if err != nil {
		t.Fatalf("PutObject SSE-C: %v", err)
	}

	if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: key, SSEC: &k1}); got != data {
		t.Errorf("SSE-C body = %q", got)
	}
	head, err := c.HeadObject(ctx, &api.HeadObjectInput{Bucket: env.Bucket, Key: key, SSEC: &k1})
	if err != nil {
		t.Fatalf("HeadObject SSE-C: %v", err)
	}
	if head.ContentLength != int64(len(data)) {
		t.Errorf("SSE-C HeadObject length = %d", head.ContentLength)
	}

	_, err = c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key})
	_ = wantS3Error(t, err, 400)
	// AWS answers a wrong key with 403; Garage uses 400.
	_, err = c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key, SSEC: &k2})
	_ = wantS3Error(t, err, 400, 403)

	// Copy re-encrypting with a different key.
	_, err = c.CopyObject(ctx, &api.CopyObjectInput{
		SrcBucket: env.Bucket, SrcKey: key, SrcSSEC: &k1,
		Bucket: env.Bucket, Key: p + "secret-copy", SSEC: &k2,
	})
	if err != nil {
		t.Fatalf("CopyObject SSE-C: %v", err)
	}
	if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: p + "secret-copy", SSEC: &k2}); got != data {
		t.Errorf("SSE-C copy body = %q", got)
	}
	_, err = c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: p + "secret-copy", SSEC: &k1})
	_ = wantS3Error(t, err, 400, 403)

	// Copy decrypting to plaintext.
	_, err = c.CopyObject(ctx, &api.CopyObjectInput{
		SrcBucket: env.Bucket, SrcKey: key, SrcSSEC: &k1,
		Bucket: env.Bucket, Key: p + "plain-copy",
	})
	if err != nil {
		t.Fatalf("CopyObject SSE-C to plain: %v", err)
	}
	if got := getString(t, c, &api.GetObjectInput{Bucket: env.Bucket, Key: p + "plain-copy"}); got != data {
		t.Errorf("plain copy body = %q", got)
	}
}

func TestAPISSECMultipart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newAPI(t)
	key := prefix(t) + "secret-multi"
	k := randomKey(t)
	sizes := []int64{api.MinPartSize, 999}

	created, err := c.CreateMultipartUpload(ctx, &api.CreateMultipartUploadInput{Bucket: env.Bucket, Key: key, SSEC: &k})
	if err != nil {
		t.Fatal(err)
	}
	var parts []api.CompletedPart
	var all []byte
	for i, n := range sizes {
		data := genBytes(byte(10+i), n)
		all = append(all, data...)
		out, err := c.UploadPart(ctx, &api.UploadPartInput{
			Bucket: env.Bucket, Key: key, UploadID: created.UploadID,
			PartNumber: i + 1, Body: bytes.NewReader(data), ContentLength: n, SSEC: &k,
		})
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		parts = append(parts, api.CompletedPart{PartNumber: i + 1, ETag: out.ETag})
	}
	_, err = c.CompleteMultipartUpload(ctx, &api.CompleteMultipartUploadInput{
		Bucket: env.Bucket, Key: key, UploadID: created.UploadID, Parts: parts,
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	get, err := c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key, SSEC: &k})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, get.Body); !bytes.Equal(got, all) {
		t.Errorf("SSE-C multipart body mismatch: got %d bytes, want %d", len(got), len(all))
	}
	_, err = c.GetObject(ctx, &api.GetObjectInput{Bucket: env.Bucket, Key: key})
	_ = wantS3Error(t, err, 400)
}
