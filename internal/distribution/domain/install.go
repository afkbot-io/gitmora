// Package domain defines exact-byte native client installation decisions.
package domain

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalid identifies malformed input or an unsafe local filesystem object.
	ErrInvalid = errors.New("invalid native installation input")
	// ErrConflict preserves an existing executable without exact replacement authority.
	ErrConflict = errors.New("native installation conflicts with existing bytes")
	// ErrBusy means another cooperating installer owns the destination lock.
	ErrBusy = errors.New("native installation is busy")
	// ErrIO is a safe local I/O failure before activation.
	ErrIO = errors.New("native installation I/O failed")
	// ErrUncertain means rename succeeded but durable directory synchronization failed.
	ErrUncertain = errors.New("native installation activation needs verification")
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Digest accepts only a canonical SHA-256 hexadecimal digest.
func Digest(value string) bool { return digestPattern.MatchString(value) }

// Replacement authorizes a create or exact-byte update; identical bytes are replay-safe.
func Replacement(current, incoming, expected string) (bool, error) {
	if !Digest(incoming) || (current != "" && !Digest(current)) || (expected != "" && !Digest(expected)) {
		return false, ErrInvalid
	}
	if current == incoming {
		return false, nil
	}
	if current != expected {
		return false, ErrConflict
	}
	return true, nil
}

// Receipt describes observed local bytes only; it contains no credential or repository state.
type Receipt struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	PreviousSHA256 string `json:"previous_sha256,omitempty"`
	Changed        bool   `json:"changed"`
}
