package main

import "runtime/debug"

// appVersion is what the About line shows: the module version the build
// stamped in (go tool mygo build), or "dev" for a plain go run build.
func appVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
