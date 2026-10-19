package test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"git.termer.net/termer/go-micro-s3/client/api"
)

const (
	garageImage  = "docker.io/dxflrs/garage:v2.4.1"
	garageRegion = "garage"
	garageBucket = "test-bucket"
)

// garage is a running single-node Garage instance.
type garage struct {
	Container testcontainers.Container
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
}

// Config returns a client configuration for the instance.
func (g *garage) Config() api.Config {
	return api.Config{
		Endpoint:        g.Endpoint,
		Region:          g.Region,
		AccessKeyID:     g.AccessKey,
		SecretAccessKey: g.SecretKey,
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func garageConfig() string {
	return fmt.Sprintf(`metadata_dir = "/tmp/meta"
data_dir = "/tmp/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "%s"

[s3_api]
s3_region = "%s"
api_bind_addr = "[::]:3900"
`, randomHex(32), garageRegion)
}

// startGarage starts a single-node Garage container with a default bucket and
// an access key that is also allowed to create buckets.
func startGarage(ctx context.Context) (g *garage, err error) {
	g = &garage{
		Region:    garageRegion,
		AccessKey: "GK" + randomHex(16),
		SecretKey: randomHex(32),
		Bucket:    garageBucket,
	}

	req := testcontainers.ContainerRequest{
		Image:        garageImage,
		Cmd:          []string{"/garage", "server", "--single-node", "--default-bucket"},
		ExposedPorts: []string{"3900/tcp"},
		Env: map[string]string{
			"GARAGE_DEFAULT_ACCESS_KEY": g.AccessKey,
			"GARAGE_DEFAULT_SECRET_KEY": g.SecretKey,
			"GARAGE_DEFAULT_BUCKET":     g.Bucket,
		},
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader(garageConfig()),
			ContainerFilePath: "/etc/garage.toml",
			FileMode:          0o600,
		}},
		WaitingFor: wait.ForLog("S3 API server listening").WithStartupTimeout(60 * time.Second),
	}
	g.Container, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start Garage container: %w", err)
	}
	defer func() {
		if err != nil {
			_ = g.Container.Terminate(context.WithoutCancel(ctx))
		}
	}()

	g.Endpoint, err = g.Container.PortEndpoint(ctx, "3900/tcp", "http")
	if err != nil {
		return nil, fmt.Errorf("failed to get Garage endpoint: %w", err)
	}

	if err := g.waitForBucket(ctx); err != nil {
		return nil, err
	}

	code, out, err := g.Container.Exec(ctx, []string{"/garage", "key", "allow", "--create-bucket", g.AccessKey})
	if err != nil || code != 0 {
		var msg []byte
		if out != nil {
			msg, _ = io.ReadAll(out)
		}
		return nil, fmt.Errorf("failed to allow bucket creation (exit %d): %v: %s", code, err, msg)
	}

	return g, nil
}

// waitForBucket polls until the default bucket is reachable with the
// default key.
func (g *garage) waitForBucket(ctx context.Context) error {
	c, err := api.New(g.Config())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		err := c.HeadBucket(ctx, &api.HeadBucketInput{Bucket: g.Bucket})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("default bucket never became ready: %w", err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
