// Package buildinfo exposes the compile-time metadata of Manager Server.
// Values are injected by release builds through Go linker flags.
package buildinfo

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// SourceCommit mirrors Commit under the name used by upstream tooling.
func SourceCommit() string { return Commit }
