//go:build !unix

package local

import (
	"context"
	"workgraph.local/platform/internal/distribution/domain"
)

// Install refuses platforms without the verified Unix stable-file/lock implementation.
func Install(context.Context, string, string, string) (domain.Receipt, error) {
	return domain.Receipt{}, domain.ErrInvalid
}
