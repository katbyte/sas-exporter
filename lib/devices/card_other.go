//go:build !(linux && (amd64 || arm64 || riscv64))

package devices

import (
	"context"
	"errors"
	"fmt"

	"github.com/katbyte/sas-exporter/lib/mpt"
)

// CardPage is one page of a card's, which is only asked for on Linux, on a machine whose pointers are eight bytes: the driver's interface is
// laid out differently on the others.
func (Dir) CardPage(context.Context, string, uint32, mpt.Page) ([]byte, error) {
	return nil, fmt.Errorf("asking a card for its own pages: %w", errors.ErrUnsupported)
}
