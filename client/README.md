# go-micro-s3

An S3 client in Go with no external dependencies.

It is designed to be much lighter and more memory-efficient than the AWS SDK while still being easy to use.
To achieve this goal, it intentionally does not implement every S3 feature and does not hash streamed upload bodies.

See [Supported Features](#supported-features).

Get it:

```bash
go get -u github.com/termermc/go-micro-s3/client
```

## Example

```go
package main

import (
	"context"
	"os"

	s3 "github.com/termermc/go-micro-s3/client"
)

const bucketName = "files"

func main() {
	client, err := s3.New(s3.Config{
		Endpoint:        os.Getenv("AWS_ENDPOINT"),
		Region:          os.Getenv("AWS_REGION"),
		AccessKeyId:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	err = client.CreateBucket(ctx, bucketName, nil)
	if err != nil {
		panic(err)
	}

	res, err := client.UploadFile(
		ctx,
		bucketName,
		"avatars/user.jpg",
		"/path/to/avatar.jpg",
		nil,
	)
	if err != nil {
		panic(err)
	}

	println("ETag: " + res.ETag)
}
```

See [example](example).

## Supported Features

Supported S3 calls:
 - CreateBucket
 - DeleteBucket
 - HeadBucket
 - ListBuckets
 - PutObject
 - DeleteObject
 - HeadObject
 - CopyObject
 - ListObjectsV2
 - CreateMultipartUpload
 - UploadPart
 - CompleteMultipartUpload
 - AbortMultipartUpload
 - ListMultipartUploads

Not all features are implemented for each call, but requests can specify extra headers to include if needed.
If you would like to implement a feature, feel free to open a pull request that includes passing tests.

SSE-C encryption is supported. Other types of encryption are not yet supported but can be used via extra headers.

**Payload signing is not supported.** This was a deliberate choice to optimize uploads. To sign an upload payload, the
entire payload needs to be buffered into memory. Even though most large files are done through multipart uploads, each
part may be dozens of MiB that would need to be buffered before upload.

Payload signing only matters when uploading using plaintext HTTP over an unreliable network. If you're using HTTPS, or
HTTP over loopback or a reliable VPN, then integrity checking payloads is redundant. Not hashing files is also just
faster for both the client and server. The server will generate an ETag for you regardless.

## Testing

The library is currently tested against [Garage](https://garagehq.deuxfleurs.fr). Garage does not support all S3
features, but it supports all the ones most people care about. If anyone is interested in adding features to the library
that Garage doesn't support, I would be happy to accept a PR that adds a [RustFS](https://rustfs.com/) or
[MinIO](https://www.min.io/) test harness.
