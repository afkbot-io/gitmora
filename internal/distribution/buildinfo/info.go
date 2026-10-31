// Package buildinfo reports explicit source release metadata without local configuration or I/O.
package buildinfo

import "runtime"

// These fields are set only by the reviewed release build's linker flags.
var version = "development"
var revision = ""

// Info identifies a standalone executable; it is not a publisher signature.
type Info struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Revision      string `json:"revision"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
}

// Current returns build-time release selection plus actual runtime platform/toolchain.
func Current() Info {
	return Info{SchemaVersion: 1, Version: version, Revision: revision, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
}
