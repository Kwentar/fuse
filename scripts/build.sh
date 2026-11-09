#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
test "$(go env GOVERSION)" = go1.27.3
export CGO_ENABLED=0 GOTOOLCHAIN=local
mkdir -p dist
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o dist/messenger.exe ./cmd/client
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o dist/bootstrap-node ./cmd/bootstrap
