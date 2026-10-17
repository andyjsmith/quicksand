// Package version holds the version Quicksand's binaries were built as.
package version

// Version is set at build time (scripts/build-*.sh): the release tag, or
// git describe. "dev" for plain go builds.
var Version = "dev"
