#!/bin/sh
# Builds the qs command-line client for common platforms, into dist/cli/.
set -eu
cd "$(dirname "$0")/.."
version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
out=dist/cli
mkdir -p "$out"
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
	os=${target%/*}
	arch=${target#*/}
	ext=
	[ "$os" = windows ] && ext=.exe
	echo "building $os/$arch"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X quicksand/internal/version.Version=$version" \
		-o "$out/qs-$os-$arch$ext" ./cmd/qs
done
echo "done: $out"
