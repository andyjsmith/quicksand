#!/bin/sh
# Builds .deb and .rpm packages into dist/packages/, from the binaries the
# other build scripts left in dist/:
#
#   ./scripts/build-packages.sh [server] [agent] [cli]    (default: all three)
set -eu
cd "$(dirname "$0")/.."
version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
# Package versions start with a digit: v1.2.3 is 1.2.3, other builds
# 0.0.0-dev.<git describe>.
case "$version" in
v[0-9]*) PKG_VERSION=${version#v} ;;
*) PKG_VERSION=0.0.0-dev.$version ;;
esac
export PKG_VERSION
out=dist/packages
# Files the package configurations take from fixed paths.
mkdir -p "$out" dist/packaging

nfpm() { go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0 "$@"; }

# pack <name> <arch> builds packaging/<name>.yaml for arch.
pack() {
	for format in deb rpm; do
		ARCH=$2 nfpm package -f "packaging/$1.yaml" -p "$format" -t "$out/"
	done
}

[ $# -gt 0 ] || set -- server agent cli
for what in "$@"; do
	case "$what" in
	server)
		sed 's,/usr/local/bin/,/usr/bin/,' deploy/server/quicksand.service >dist/packaging/quicksand.service
		pack quicksand amd64
		;;
	agent)
		for arch in amd64 arm64; do
			cp "dist/agent/quicksand-agent-linux-$arch" dist/packaging/quicksand-agent
			pack quicksand-agent "$arch"
		done
		;;
	cli)
		for shell in bash fish zsh; do
			go run ./cmd/qs completion "$shell" >"dist/packaging/qs.$shell"
		done
		mv dist/packaging/qs.zsh dist/packaging/_qs
		for arch in amd64 arm64; do
			cp "dist/cli/qs-linux-$arch" dist/packaging/qs
			pack quicksand-cli "$arch"
		done
		;;
	*)
		echo "unknown package: $what" >&2
		exit 1
		;;
	esac
done
echo "built $PKG_VERSION: $out"
