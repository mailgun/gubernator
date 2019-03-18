#! /bin/sh

# Make sure the script fails fast.
set -e
set -u
set -x

PROTO_DIR=proto
GO_DIR=golang
PY_DIR=python/gubernator
GRPC_GATEWAY_DIR=$GOPATH/pkg/mod/github.com/grpc-ecosystem/grpc-gateway\@v1.7.0/third_party/googleapis

protoc -I=$PROTO_DIR \
    -I=$GRPC_GATEWAY_DIR \
    --go_out=plugins=grpc:$GO_DIR \
    $PROTO_DIR/*.proto

protoc -I=$PROTO_DIR \
    -I=$GRPC_GATEWAY_DIR \
    --grpc-gateway_out=logtostderr=true:$GO_DIR \
    $PROTO_DIR/*.proto

python3 -m grpc_tools.protoc \
    -I=$PROTO_DIR \
    -I=$GRPC_GATEWAY_DIR \
    --python_out=$PY_DIR \
    --grpc_python_out=$PY_DIR \
    $PROTO_DIR/*.proto

touch $PY_DIR/__init__.py

# Fix import paths because python3
./scripts/fix-import.py
