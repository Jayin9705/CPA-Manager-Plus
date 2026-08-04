// Package buildinfo exposes the compile-time metadata of Manager Server.
package buildinfo

// These values are overridden by release builds through Go linker flags.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
