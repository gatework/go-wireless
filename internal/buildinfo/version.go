// Package buildinfo exposes the release identity without reading configuration.
package buildinfo

import "runtime/debug"

// Identity is set by the release linker to the version and exact source commit.
var Identity string

// String also identifies ordinary source builds and go install module@version.
func String(version string) string {
	if Identity != "" {
		return Identity
	}
	version = "v" + version
	revision, dirty := "unknown", false
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == "github.com/gatework/go-wireless" {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
	}
	if dirty {
		revision += "-dirty"
	}
	return "go-wireless " + version + " revision " + revision
}
