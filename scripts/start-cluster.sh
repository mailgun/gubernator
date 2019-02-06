#! /bin/sh

# Make sure the script fails fast.
set -e
set -u
set -x

go run ./cmd/gubernator-server  --config etc/server-1.yaml &
go run ./cmd/gubernator-server  --config etc/server-2.yaml &
go run ./cmd/gubernator-server  --config etc/server-3.yaml &
