package main

import (
	"runtime/debug"
	"strings"
)

// version is set by release builds:
//
//	go build -ldflags "-X main.version=1.2.3"
//
// Other builds leave it empty. versionString then reports the module
// version that Go stamps from git (Go 1.24 and later): the tag on a
// tagged commit, otherwise a pseudo-version such as
// 1.2.4-0.20261102093000-abcdef123456, with "+dirty" for uncommitted
// changes.
var version string

// versionString returns the version without a leading "v".
func versionString() string {
	if version != "" {
		return strings.TrimPrefix(version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "devel"
}
