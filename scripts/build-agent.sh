#!/bin/sh
# Builds the guest agent for the platforms templates use, into dist/agent/.
set -eu
cd "$(dirname "$0")/.."
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
out=dist/agent
mkdir -p "$out"
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
	os=${target%/*}
	arch=${target#*/}
	ext=
	[ "$os" = windows ] && ext=.exe
	echo "building $os/$arch"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X quicksand/internal/agent.Version=$version" \
		-o "$out/quicksand-agent-$os-$arch$ext" ./cmd/quicksand-agent
done
echo "done: $out"
