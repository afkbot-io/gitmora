//go:build unix

package runtimeconfig

import (
	"os"
	"syscall"
)

// Nonblocking open plus descriptor validation rejects a regular-path-to-FIFO replacement without hanging startup.
func openSecretFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
