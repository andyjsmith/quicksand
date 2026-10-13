#!/bin/sh
# Builds the Quicksand server with the web client embedded, into dist/server/.
set -eu
cd "$(dirname "$0")/.."
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
out=dist/server
mkdir -p "$out"
(cd web && npm ci --silent && npm run build --silent)
CGO_ENABLED=0 GOOS=${GOOS:-linux} GOARCH=${GOARCH:-amd64} go build -trimpath -tags embedweb \
	-ldflags "-s -w" -o "$out/quicksand" ./cmd/quicksand
cp deploy/server/* "$out/"
echo "built $version: $out"
