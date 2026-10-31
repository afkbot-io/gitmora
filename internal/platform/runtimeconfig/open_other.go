//go:build !unix

package runtimeconfig

import "os"

func openSecretFile(path string) (*os.File, error) { return os.Open(path) }
