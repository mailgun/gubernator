#!/bin/bash

# Make sure the script fails fast.
set -eux

SCRIPT_PATH=$(dirname "$0")                  # relative
REPO_ROOT=$(cd "${SCRIPT_PATH}/.." && pwd )  # absolutized and normalized
PROTO_DIR=$REPO_ROOT/proto
PYTHON_DST_DIR=$REPO_ROOT/python/gubernator

GOOGLE_APIS_DIR=$REPO_ROOT/googleapis
if [ -d $GOOGLE_APIS_DIR ]
then
    pushd $GOOGLE_APIS_DIR
    git pull
    popd
else
    git clone https://github.com/googleapis/googleapis $GOOGLE_APIS_DIR
fi

# Build Python stabs
pip install -r "${REPO_ROOT}/python/requirements.txt"
python3 -m grpc.tools.protoc \
    -I=$PROTO_DIR \
    -I=$GOOGLE_APIS_DIR \
    --python_out=$PYTHON_DST_DIR \
    --pyi_out=$PYTHON_DST_DIR \
    --grpc_python_out=$PYTHON_DST_DIR \
    --mypy_grpc_out=$PYTHON_DST_DIR \
    $PROTO_DIR/*.proto
# Rewrite imports to import from package
sed -i'' -e 's/import gubernator_pb2/from gubernator import gubernator_pb2/' "${PYTHON_DST_DIR}/"gubernator_pb2_grpc.py*
sed -i'' -e 's/import gubernator_pb2/from gubernator import gubernator_pb2/' "${PYTHON_DST_DIR}/"peers_pb2.py*
sed -i'' -e 's/import peers_pb2/from gubernator import peers_pb2/' "${PYTHON_DST_DIR}/"peers_pb2_grpc.py*
