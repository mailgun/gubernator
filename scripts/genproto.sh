#! /bin/sh

# Copyright 2018-2022 Mailgun Technologies Inc
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

# Make sure the script fails fast.
set -eux

REPO_ROOT=$(git rev-parse --show-toplevel)
PROTO_DIR=$REPO_ROOT/proto
VENDOR_DIR=$REPO_ROOT/proto_vendor
PYTHON_DST_DIR=$REPO_ROOT/python/gubernator
GOLANG_DST_DIR=$REPO_ROOT

# Pull dependencies.
GOOGLE_APIS_DIR=$REPO_ROOT/proto_vendor/googleapis
if [ ! -d $GOOGLE_APIS_DIR ]; then
  git clone --depth 1 https://github.com/googleapis/googleapis $GOOGLE_APIS_DIR
fi

# Build Golang stubs
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
go install google.golang.org/protobuf/cmd/protoc-gen-go \
  google.golang.org/grpc/cmd/protoc-gen-go-grpc \
  github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
  github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2
GOPATH=$(go env GOPATH)
export PATH=$PATH:$GOPATH/bin

protoc -I=$PROTO_DIR \
  -I=$GOOGLE_APIS_DIR \
  --go_out=$GOLANG_DST_DIR \
  --go_opt=paths=source_relative \
  --go-grpc_out=$GOLANG_DST_DIR \
  --go-grpc_opt=paths=source_relative \
  $PROTO_DIR/*.proto

protoc -I=$PROTO_DIR \
  -I=$GOOGLE_APIS_DIR \
  --grpc-gateway_out=$GOLANG_DST_DIR \
  --grpc-gateway_opt=logtostderr=true \
  --grpc-gateway_opt=paths=source_relative \
  --grpc-gateway_opt=generate_unbound_methods=true \
  $PROTO_DIR/*.proto

# Build Python stubs
mkdir -p "$PYTHON_DST_DIR"
pip install grpcio grpcio_tools
python3 -m grpc.tools.protoc \
  -I=$PROTO_DIR \
  -I=$GOOGLE_APIS_DIR \
  --python_out=$PYTHON_DST_DIR \
  --pyi_out=$PYTHON_DST_DIR \
  --grpc_python_out=$PYTHON_DST_DIR \
  $PROTO_DIR/*.proto
touch $PYTHON_DST_DIR/__init__.py
