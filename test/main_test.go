package test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"git.termer.net/termer/go-micro-s3/client"
	"git.termer.net/termer/go-micro-s3/client/api"
)

// env is the Garage instance shared by all tests in the package.
var env *garage

func TestMain(m *testing.M) {
	ctx := context.Background()
	g, err := startGarage(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	env = g

	code := m.Run()

	if err := g.Container.Terminate(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "failed to terminate Garage container:", err)
	}
	os.Exit(code)
}

func newAPI(t *testing.T) *api.Client {
	t.Helper()
	c, err := api.New(env.Config())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newClient(t *testing.T, opts ...client.Option) *client.Client {
	t.Helper()
	c, err := client.New(env.Config(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// prefix returns a unique key prefix for the calling test.
func prefix(t *testing.T) string {
	return strings.ReplaceAll(t.Name(), "/", "_") + "-" + randomHex(4) + "/"
}

// bucketName returns a unique, valid bucket name.
func bucketName() string {
	return "b-" + randomHex(8)
}

func randomKey(t *testing.T) api.SseCustomerKey {
	t.Helper()
	k, err := api.NewSseCustomerKey([]byte(randomHex(16)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// genReader deterministically generates n pseudo-random bytes on the fly
// without holding them in memory.
type genReader struct {
	rng *rand.ChaCha8
	n   int64
}

func newGenReader(seed byte, n int64) *genReader {
	var s [32]byte
	s[0] = seed
	return &genReader{rng: rand.NewChaCha8(s), n: n}
}

func (g *genReader) Read(p []byte) (int, error) {
	if g.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > g.n {
		p = p[:g.n]
	}
	n, _ := g.rng.Read(p)
	g.n -= int64(n)
	return n, nil
}

func genSum(seed byte, n int64) [32]byte {
	h := sha256.New()
	_, _ = io.Copy(h, newGenReader(seed, n))
	var s [32]byte
	copy(s[:], h.Sum(nil))
	return s
}

func genBytes(seed byte, n int64) []byte {
	b, _ := io.ReadAll(newGenReader(seed, n))
	return b
}

func sumReader(t *testing.T, r io.Reader) ([32]byte, int64) {
	t.Helper()
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		t.Fatal(err)
	}
	var s [32]byte
	copy(s[:], h.Sum(nil))
	return s, n
}

func readAll(t *testing.T, r io.ReadCloser) []byte {
	t.Helper()
	defer func() {
		_ = r.Close()
	}()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// onlyReader hides every method of an io.Reader except Read.
type onlyReader struct{ r io.Reader }

func (o onlyReader) Read(p []byte) (int, error) { return o.r.Read(p) }
