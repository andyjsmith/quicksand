#!/bin/sh
# Packages the "Open in Quicksand" browser extension for Chrome (and other
# Chromium browsers) and Firefox, into dist/extension/. They share their
# files; only the manifest's background and Firefox's add-on ID differ.
set -eu
cd "$(dirname "$0")/.."
version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
# Manifests need a version of up to four numbers: v1.2.3-rc.1 is 1.2.3, and
# untagged builds are 0.0.0.
number=$(printf '%s\n' "$version" | sed -n 's/^v\{0,1\}\([0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}\).*/\1/p')
number=${number:-0.0.0}
out=dist/extension
rm -rf "$out"
mkdir -p "$out"
for browser in chrome firefox; do
	dir=$out/$browser
	mkdir -p "$dir"
	cp -r extension/background.js extension/options.html extension/options.js extension/options.css extension/icons "$dir/"
	case $browser in
	chrome)
		jq --arg v "$number" --arg name "$version" \
			'.version = $v | .version_name = $name | .background = {service_worker: "background.js"}' \
			extension/manifest.json >"$dir/manifest.json"
		;;
	firefox)
		jq --arg v "$number" \
			'.version = $v | .background = {scripts: ["background.js"]} |
			.browser_specific_settings = {gecko: {
				id: "open-in-quicksand@andyjsmith.github.io",
				strict_min_version: "128.0",
				data_collection_permissions: {required: ["none"]}
			}}' \
			extension/manifest.json >"$dir/manifest.json"
		;;
	esac
	(cd "$dir" && zip -qr -X "../quicksand-extension-$browser.zip" .)
	echo "built $out/quicksand-extension-$browser.zip ($number)"
done
