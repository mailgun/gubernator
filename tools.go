//go:build tools

package gubernator

import (
	_ "cloud.google.com/go/compute"
	_ "cloud.google.com/go/compute/metadata"
	_ "github.com/golang/groupcache"
	_ "github.com/google/s2a-go"
	_ "github.com/google/uuid"
	_ "github.com/googleapis/enterprise-certificate-proxy"
	_ "github.com/googleapis/gax-go"
	_ "go.opencensus.io"
	_ "golang.org/x/crypto"
	_ "google.golang.org/api"
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
)
