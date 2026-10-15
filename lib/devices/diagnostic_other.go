//go:build !linux

package devices

import (
	"context"
	"errors"
	"fmt"
)

// Diagnostic is one page of an enclosure's diagnostics, which only the Linux kernel is asked for.
func (Dir) Diagnostic(context.Context, string, byte) ([]byte, error) {
	return nil, fmt.Errorf("asking an enclosure for its pages: %w", errors.ErrUnsupported)
}
