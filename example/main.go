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
