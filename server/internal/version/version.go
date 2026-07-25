// Package version exposes build metadata that is injected at link time via
// -ldflags. It is the single source of truth for the running build's identity.
package version

import "runtime"

// These variables are overridden at build time with:
//
//	-ldflags "-X github.com/bumshi/bumshi/server/internal/version.Version=..."
//
// Their zero values are deliberately obvious so an un-stamped binary is easy to
// spot in the field.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Info is a serialisable snapshot of the build's identity.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns the current build information.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
