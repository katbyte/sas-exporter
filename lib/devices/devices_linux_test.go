//go:build linux

package devices

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// file is a file of that name in a directory of the test's own, standing where a device would.
func file(t *testing.T, name string, mode os.FileMode) Dir {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), nil, mode); err != nil {
		t.Fatal(err)
	}

	return Dir(dir)
}

// noDevice says an error is the kernel saying a file is not a device that takes such a request. A processor that is only being imitated, as one
// is when a test for another kind of machine is run, says instead that it cannot pass the request on at all.
func noDevice(err error) bool {
	return errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.ENOSYS)
}

// The request does reach the kernel: handed a file that is no such device, the kernel says it is not one, and that comes back as an error
// naming the file rather than as a page. It is as far as a machine with no enclosure and no card can go.
func TestAskingAFileThatIsNoDevice(t *testing.T) {
	t.Parallel()

	if _, err := file(t, "sg26", 0o600).Diagnostic(t.Context(), "sg26", 0x01); !noDevice(err) || !strings.Contains(err.Error(), "sg26") {
		t.Errorf("an enclosure's pages asked of a plain file = %v, want the kernel saying it is no such device, and the file named", err)
	}
	if got, err := file(t, cardDevice, 0o600).CardPage(t.Context(), cardDriver, 0); got != nil || !noDevice(err) || !strings.Contains(err.Error(), cardDevice) {
		t.Errorf("a card's page asked of a plain file = % x (%v), want the kernel saying it is no such device, and the file named", got, err)
	}
}

// A device there is no leave to open is said as that, so the program can tell being kept out from something going wrong.
func TestADeviceThereIsNoLeaveToOpen(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root is never kept out of a file")
	}

	if _, err := file(t, "sg26", 0).Diagnostic(t.Context(), "sg26", 0x01); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("an enclosure's device that cannot be opened = %v, want leave refused", err)
	}
	if _, err := file(t, cardDevice, 0).CardPage(t.Context(), cardDriver, 0); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("the cards' device that cannot be opened = %v, want leave refused", err)
	}
}

// Nothing is opened for a name that is not one file's, for a card on a driver this was not written against, or once the round has been stopped.
func TestWhatIsNeverAsked(t *testing.T) {
	t.Parallel()

	dir := file(t, cardDevice, 0o600)
	if _, err := dir.Diagnostic(t.Context(), "../"+cardDevice, 0x01); err == nil || noDevice(err) {
		t.Errorf("a device named with a path in it = %v, want it refused before anything is opened", err)
	}
	if _, err := dir.CardPage(t.Context(), "mpt2sas", 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("a card on another driver = %v, want it said that such a card is not asked", err)
	}

	stopped, stop := context.WithCancel(t.Context())
	stop()
	if _, err := dir.CardPage(stopped, cardDriver, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("a card asked once the round was stopped = %v, want it not asked", err)
	}
	if _, err := dir.Diagnostic(stopped, cardDevice, 0x01); !errors.Is(err, context.Canceled) {
		t.Errorf("an enclosure asked once the round was stopped = %v, want it not asked", err)
	}
}
