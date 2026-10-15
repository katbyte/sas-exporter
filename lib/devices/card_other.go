//go:build !(linux && (amd64 || arm64 || riscv64))

package devices

import (
	"context"
	"errors"
	"fmt"
)

// CardPage is the page a card keeps its own temperature in, which is only asked for on Linux, on a machine whose pointers are eight bytes: the
// driver's interface is laid out differently on the others.
func (Dir) CardPage(context.Context, string, uint32) ([]byte, error) {
	return nil, fmt.Errorf("asking a card for its temperature: %w", errors.ErrUnsupported)
}
