#! /bin/sh

# Copyright 2018-2019 Mailgun Technologies Inc
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

SCRIPT_PATH=$(dirname "$0")                  # relative
REPO_ROOT=$(cd "${SCRIPT_PATH}/.." && pwd )  # absolutized and normalized
PROTO_DIR=$REPO_ROOT/proto
PYTHON_DST_DIR=$REPO_ROOT/python/gubernator
GOLANG_DST_DIR=$REPO_ROOT

# Build Golang stabs
go get google.golang.org/protobuf/cmd/protoc-gen-go
go install google.golang.org/protobuf/cmd/protoc-gen-go
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc
go get github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway
go install github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway
GOPATH=$(go env GOPATH)
export PATH=$PATH:$GOPATH/bin

GOOGLE_APIS_DIR=$REPO_ROOT/googleapis
if [ -d $GOOGLE_APIS_DIR ]
then
    pushd $GOOGLE_APIS_DIR
    git pull
    popd
else
    git clone https://github.com/googleapis/googleapis $GOOGLE_APIS_DIR
fi

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

# Build Python stabs
mkdir -p "$PYTHON_DST_DIR"
pip install grpcio
pip install grpcio-tools
python -m grpc.tools.protoc \
    -I=$PROTO_DIR \
    -I=$GOOGLE_APIS_DIR \
    --python_out=$PYTHON_DST_DIR \
    --grpc_python_out=$PYTHON_DST_DIR \
    $PROTO_DIR/*.proto
touch $PYTHON_DST_DIR/__init__.py
