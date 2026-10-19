# go-micro-s3

An S3 client in Go with no external dependencies.

TODO Document that it doesn't support payload hashing. This means that it must be run over HTTPS or a guaranteed
reliable plaintext network (like loopback, or HTTP over a VPN).
