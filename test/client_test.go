package test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"git.termer.net/termer/go-micro-s3/client"
	"git.termer.net/termer/go-micro-s3/client/api"
)

const mib = 1 << 20

// multipartOpts makes anything over 5 MiB a multipart upload with 5 MiB parts.
var multipartOpts = []client.Option{
	client.WithPartSize(5 * mib),
	client.WithMultipartThreshold(5 * mib),
}

func checkDownload(t *testing.T, c *client.Client, key string, seed byte, size int64, opts *client.DownloadOptions) {
	t.Helper()
	body, info, err := c.Download(context.Background(), env.Bucket, key, opts)
	if err != nil {
		t.Fatalf("Download(%q): %v", key, err)
	}
	defer func() {
		_ = body.Close()
	}()
	if info.Size != size {
		t.Errorf("Download info size = %d, want %d", info.Size, size)
	}
	sum, n := sumReader(t, body)
	if n != size || sum != genSum(seed, size) {
		t.Errorf("Download(%q) content mismatch: got %d bytes", key, n)
	}
}

func noUploadsLeft(t *testing.T, c *client.Client, key string) {
	t.Helper()
	out, err := c.Api().ListMultipartUploads(context.Background(), &api.ListMultipartUploadsInput{Bucket: env.Bucket, Prefix: key})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Uploads) != 0 {
		t.Errorf("multipart uploads left behind: %+v", out.Uploads)
	}
}

func TestClientUploadSmall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newClient(t)
	key := prefix(t) + "small.txt"
	data := "small upload"

	res, err := c.Upload(ctx, env.Bucket, key, strings.NewReader(data), int64(len(data)), &client.UploadOptions{
		ContentType: "text/plain",
		Metadata:    map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.ETag == "" || strings.Contains(res.ETag, "-") {
		t.Errorf("single-PUT ETag = %q", res.ETag)
	}

	info, err := c.Stat(ctx, env.Bucket, key, nil)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(data)) || info.ContentType != "text/plain" || info.Metadata["k"] != "v" || info.ETag != res.ETag {
		t.Errorf("Stat = %+v", info)
	}

	var buf bytes.Buffer
	n, err := c.DownloadTo(ctx, env.Bucket, key, &buf, nil)
	if err != nil {
		t.Fatalf("DownloadTo: %v", err)
	}
	if n != int64(len(data)) || buf.String() != data {
		t.Errorf("DownloadTo = %d %q", n, buf.String())
	}

	body, _, err := c.Download(ctx, env.Bucket, key, &client.DownloadOptions{Range: "bytes=6-"})
	if err != nil {
		t.Fatalf("Download range: %v", err)
	}
	if got := string(readAll(t, body)); got != "upload" {
		t.Errorf("ranged download = %q", got)
	}
}

func TestClientUploadEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newClient(t)
	key := prefix(t) + "empty"
	if _, err := c.Upload(ctx, env.Bucket, key, nil, 0, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	info, err := c.Stat(ctx, env.Bucket, key, nil)
	if err != nil || info.Size != 0 {
		t.Fatalf("Stat = %+v, %v", info, err)
	}
}

func TestClientUploadMultipartSequential(t *testing.T) {
	t.Parallel()
	c := newClient(t, multipartOpts...)
	key := prefix(t) + "seq"
	size := int64(17*mib + 123)

	// onlyReader hides io.ReaderAt, forcing sequential part reads.
	res, err := c.Upload(context.Background(), env.Bucket, key, onlyReader{newGenReader(1, size)}, size, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.HasSuffix(strings.Trim(res.ETag, `"`), "-4") {
		t.Errorf("multipart ETag = %q, want 4 parts", res.ETag)
	}
	checkDownload(t, c, key, 1, size, nil)
}

func TestClientUploadMultipartParallel(t *testing.T) {
	t.Parallel()
	c := newClient(t, append(multipartOpts, client.WithConcurrency(3))...)
	key := prefix(t) + "par"
	size := int64(17*mib + 123)
	const skip = 7

	// Write some leading junk followed by the payload, then seek past the
	// junk so the upload must honour the file's current offset.
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()
	if _, err := f.Write(make([]byte, skip)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, newGenReader(2, size)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(skip, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	res, err := c.Upload(context.Background(), env.Bucket, key, f, size, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.HasSuffix(strings.Trim(res.ETag, `"`), "-4") {
		t.Errorf("multipart ETag = %q, want 4 parts", res.ETag)
	}
	if off, _ := f.Seek(0, io.SeekCurrent); off != skip+size {
		t.Errorf("file offset after upload = %d, want %d", off, skip+size)
	}
	checkDownload(t, c, key, 2, size, nil)
}

// TestClientUploadNoBuffering is deliberately not parallel so that other tests
// don't skew the allocation count.
func TestClientUploadNoBuffering(t *testing.T) {
	c := newClient(t, append(multipartOpts, client.WithConcurrency(1))...)
	key := prefix(t) + "big"
	size := int64(128 * mib)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := c.Upload(context.Background(), env.Bucket, key, newGenReader(3, size), size, nil)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d KiB while uploading %d MiB", allocated>>10, size>>20)
	if allocated > 4*mib {
		t.Errorf("Upload allocated %d bytes for a %d byte object; it appears to buffer", allocated, size)
	}
	checkDownload(t, c, key, 3, size, nil)
}

func TestClientUploadShortReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := prefix(t)
	claimed := int64(12 * mib)
	actual := int64(7 * mib)

	t.Run("single", func(t *testing.T) {
		c := newClient(t)
		_, err := c.Upload(ctx, env.Bucket, p+"single", newGenReader(4, 10), 20, nil)
		if err == nil {
			t.Fatal("Upload with short reader succeeded")
		}
		if ok, _ := c.Exists(ctx, env.Bucket, p+"single"); ok {
			t.Error("object exists after failed upload")
		}
	})
	t.Run("sequential", func(t *testing.T) {
		c := newClient(t, multipartOpts...)
		_, err := c.Upload(ctx, env.Bucket, p+"seq", onlyReader{newGenReader(4, actual)}, claimed, nil)
		if err == nil {
			t.Fatal("Upload with short reader succeeded")
		}
		noUploadsLeft(t, c, p+"seq")
	})
	t.Run("parallel", func(t *testing.T) {
		c := newClient(t, multipartOpts...)
		r := bytes.NewReader(genBytes(4, actual))
		_, err := c.Upload(ctx, env.Bucket, p+"par", r, claimed, nil)
		if err == nil {
			t.Fatal("Upload with short reader succeeded")
		}
		noUploadsLeft(t, c, p+"par")
	})
}

func TestClientUploadCanceled(t *testing.T) {
	t.Parallel()
	c := newClient(t, multipartOpts...)
	key := prefix(t) + "canceled"
	size := int64(12 * mib)

	ctx, cancel := context.WithCancel(context.Background())
	r := &cancelAfter{r: newGenReader(5, size), n: 6 * mib, cancel: cancel}
	_, err := c.Upload(ctx, env.Bucket, key, r, size, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Upload error = %v, want context.Canceled", err)
	}
	noUploadsLeft(t, c, key)
}

// cancelAfter calls cancel once n bytes have been read.
type cancelAfter struct {
	r      io.Reader
	n      int64
	cancel func()
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n -= int64(n)
	if c.n <= 0 {
		c.cancel()
	}
	return n, err
}

func TestClientSSEC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newClient(t, multipartOpts...)
	p := prefix(t)
	k := randomKey(t)
	opts := &client.UploadOptions{SseC: &k}

	for _, tc := range []struct {
		name string
		seed byte
		size int64
	}{
		{"single", 6, 1000},
		{"multipart", 7, 11*mib + 5},
	} {
		key := p + tc.name
		if _, err := c.Upload(ctx, env.Bucket, key, newGenReader(tc.seed, tc.size), tc.size, opts); err != nil {
			t.Fatalf("%s: Upload: %v", tc.name, err)
		}
		checkDownload(t, c, key, tc.seed, tc.size, &client.DownloadOptions{SseC: &k})

		info, err := c.Stat(ctx, env.Bucket, key, &client.StatOptions{SseC: &k})
		if err != nil || info.Size != tc.size {
			t.Errorf("%s: Stat = %+v, %v", tc.name, info, err)
		}
		if _, _, err := c.Download(ctx, env.Bucket, key, nil); err == nil {
			t.Errorf("%s: Download without key succeeded", tc.name)
		}
		wrong := randomKey(t)
		if _, _, err := c.Download(ctx, env.Bucket, key, &client.DownloadOptions{SseC: &wrong}); err == nil {
			t.Errorf("%s: Download with wrong key succeeded", tc.name)
		}
	}

	// Copy with re-encryption.
	k2 := randomKey(t)
	err := c.Copy(ctx,
		client.ObjectRef{Bucket: env.Bucket, Key: p + "single"},
		client.ObjectRef{Bucket: env.Bucket, Key: p + "copy"},
		&client.CopyOptions{SrcSseC: &k, SseC: &k2})
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	checkDownload(t, c, p+"copy", 6, 1000, &client.DownloadOptions{SseC: &k2})
}

func TestClientObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newClient(t)
	p := prefix(t)

	if _, err := c.Stat(ctx, env.Bucket, p+"missing", nil); !api.IsNotFound(err) {
		t.Errorf("Stat missing: %v", err)
	}
	if ok, err := c.Exists(ctx, env.Bucket, p+"missing"); ok || err != nil {
		t.Errorf("Exists missing = %v, %v", ok, err)
	}
	if _, _, err := c.Download(ctx, env.Bucket, p+"missing", nil); !api.IsNotFound(err) {
		t.Errorf("Download missing: %v", err)
	}

	keys := []string{"a", "b", "c", "d/1", "d/2", "e"}
	for _, k := range keys {
		if _, err := c.Upload(ctx, env.Bucket, p+k, strings.NewReader(k), int64(len(k)), nil); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := c.Exists(ctx, env.Bucket, p+"a"); !ok || err != nil {
		t.Errorf("Exists = %v, %v", ok, err)
	}

	// Paginated listing.
	var listed []string
	for e, err := range c.List(ctx, env.Bucket, &client.ListOptions{Prefix: p, PageSize: 2}) {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if e.IsPrefix || e.Size != int64(len(strings.TrimPrefix(e.Key, p))) {
			t.Errorf("List entry %+v", e)
		}
		listed = append(listed, strings.TrimPrefix(e.Key, p))
	}
	if !slices.Equal(listed, keys) {
		t.Errorf("List = %q, want %q", listed, keys)
	}

	// Early break stops iteration.
	count := 0
	for range c.List(ctx, env.Bucket, &client.ListOptions{Prefix: p, PageSize: 2}) {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Errorf("early break count = %d", count)
	}

	// Directory-style listing.
	var objs, dirs []string
	for e, err := range c.List(ctx, env.Bucket, &client.ListOptions{Prefix: p, Delimiter: "/"}) {
		if err != nil {
			t.Fatal(err)
		}
		if e.IsPrefix {
			dirs = append(dirs, strings.TrimPrefix(e.Key, p))
		} else {
			objs = append(objs, strings.TrimPrefix(e.Key, p))
		}
	}
	if want := []string{"a", "b", "c", "e"}; !slices.Equal(objs, want) {
		t.Errorf("delimiter objects = %q, want %q", objs, want)
	}
	if want := []string{"d/"}; !slices.Equal(dirs, want) {
		t.Errorf("delimiter prefixes = %q, want %q", dirs, want)
	}

	// Copy with metadata replacement.
	err := c.Copy(ctx,
		client.ObjectRef{Bucket: env.Bucket, Key: p + "a"},
		client.ObjectRef{Bucket: env.Bucket, Key: p + "a-copy"},
		&client.CopyOptions{ReplaceMetadata: true, ContentType: "text/x-copy", Metadata: map[string]string{"copied": "true"}})
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	info, err := c.Stat(ctx, env.Bucket, p+"a-copy", nil)
	if err != nil || info.ContentType != "text/x-copy" || info.Metadata["copied"] != "true" || info.Size != 1 {
		t.Errorf("copied Stat = %+v, %v", info, err)
	}

	if err := c.Delete(ctx, env.Bucket, p+"a-copy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := c.Exists(ctx, env.Bucket, p+"a-copy"); ok {
		t.Error("object exists after Delete")
	}

	var all []string
	for _, k := range keys {
		all = append(all, p+k)
	}
	if err := c.DeleteMany(ctx, env.Bucket, all); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	for e, err := range c.List(ctx, env.Bucket, &client.ListOptions{Prefix: p}) {
		t.Errorf("object remains after DeleteMany: %+v, %v", e, err)
	}
}

func TestClientBuckets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newClient(t)
	name := bucketName()

	if ok, err := c.BucketExists(ctx, name); ok || err != nil {
		t.Fatalf("BucketExists before create = %v, %v", ok, err)
	}
	if err := c.CreateBucket(ctx, name); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if ok, err := c.BucketExists(ctx, name); !ok || err != nil {
		t.Fatalf("BucketExists after create = %v, %v", ok, err)
	}
	buckets, err := c.Buckets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(buckets, func(b api.BucketInfo) bool { return b.Name == name }) {
		t.Errorf("Buckets missing %q", name)
	}

	// A non-empty bucket can't be deleted.
	if _, err = c.Upload(ctx, name, "obj", strings.NewReader("x"), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteBucket(ctx, name); err == nil {
		t.Error("DeleteBucket on non-empty bucket succeeded")
	}
	if err = c.Delete(ctx, name, "obj"); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteBucket(ctx, name); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if ok, err := c.BucketExists(ctx, name); ok || err != nil {
		t.Errorf("BucketExists after delete = %v, %v", ok, err)
	}
}
