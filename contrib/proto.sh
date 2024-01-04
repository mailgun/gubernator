#!/bin/sh
# This script assumes that protoc is installed and available on the PATH.

# Make sure the script fails fast.
set -eux

SCRIPT_PATH=$(dirname "$0")                 # relative
REPO_ROOT=$(cd "${SCRIPT_PATH}/.." && pwd)  # absolutized and normalized
SRC_DIR=$REPO_ROOT
PYTHON_DST_DIR=$REPO_ROOT/python/gubernator
GOLANG_DST_DIR=$REPO_ROOT

# Build Golang stabs
go get google.golang.org/protobuf/cmd/protoc-gen-go
go install google.golang.org/protobuf/cmd/protoc-gen-go
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc

mkdir -p "$GOLANG_DST_DIR"
go install google.golang.org/protobuf/cmd/protoc-gen-go
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc
echo "PATH=$PATH"
export PATH=$PATH:$(go env GOPATH)/bin
protoc -I=$SRC_DIR \
    -I=$SRC_DIR/googleapis \
    --go_out=$GOLANG_DST_DIR \
    --go_opt=paths=source_relative \
    --go-grpc_out=$GOLANG_DST_DIR \
    --go-grpc_opt=paths=source_relative \
    $SRC_DIR/*.proto

# Build Python stabs
mkdir -p "$PYTHON_DST_DIR"
pip3 install grpcio
pip3 install grpcio-tools
python3 -m grpc.tools.protoc \
    -I=$SRC_DIR \
    -I=$SRC_DIR/googleapis \
    --python_out=$PYTHON_DST_DIR \
    --grpc_python_out=$PYTHON_DST_DIR \
    $SRC_DIR/*.proto
touch $PYTHON_DST_DIR/__init__.py
