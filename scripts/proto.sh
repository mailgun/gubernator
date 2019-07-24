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
