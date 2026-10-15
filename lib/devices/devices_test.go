package devices

import (
	"bytes"
	"errors"
	"io/fs"
	"runtime"
	"testing"
)

// The six bytes that ask an enclosure for a page are the ones the standard gives: the command, that it names a page, which page, and how much
// room there is for the answer.
func TestTheCommandThatAsksForAPage(t *testing.T) {
	t.Parallel()

	if got, want := diagnosticCommand(0x02), [6]byte{0x1c, 0x01, 0x02, 0xff, 0xfc, 0x00}; got != want {
		t.Errorf("the command is % x, want % x", got, want)
	}
}

// What comes back is as long as the room given, and the page says in its own header how much of that is the page.
func TestAPageIsAsLongAsItSays(t *testing.T) {
	t.Parallel()

	sent := append([]byte{0x02, 0x00, 0x00, 0x04, 1, 2, 3, 4}, make([]byte, 100)...)
	if got, err := diagnosticPage(0x02, sent); err != nil || !bytes.Equal(got, sent[:8]) {
		t.Errorf("the page is % x (%v), want its header and the four bytes it says follow", got, err)
	}

	// a page that says it is longer than what arrived is what arrived
	if got, err := diagnosticPage(0x02, []byte{0x02, 0x00, 0x01, 0x00, 1, 2}); err != nil || len(got) != 6 {
		t.Errorf("a page cut short is %d bytes (%v), want the 6 that arrived", len(got), err)
	}
	if _, err := diagnosticPage(0x02, []byte{0x02, 0x00}); err == nil {
		t.Error("two bytes were taken as a page")
	}
}

// The name of a device comes from the kernel, and is still never let lead anywhere but the one directory.
func TestADeviceIsNamedPlainly(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{"sg26": true, "mpt3ctl": true, "": false, ".": false, "..": false, "../sda": false, "bsg/1:0:24:0": false, `a\b`: false} {
		if plain(name) != want {
			t.Errorf("plain(%q) = %t, want %t", name, !want, want)
		}
	}
}

// A device that is not there, which is what a container given no devices sees, is said as that, so the program can tell being kept out from
// something going wrong. Off Linux nothing is asked at all.
func TestADeviceThatIsNotThere(t *testing.T) {
	t.Parallel()

	dir := Dir(t.TempDir())
	want := fs.ErrNotExist
	if runtime.GOOS != "linux" {
		want = errors.ErrUnsupported
	}

	if _, err := dir.Diagnostic(t.Context(), "sg26", 0x01); !errors.Is(err, want) {
		t.Errorf("an enclosure with no device = %v, want %v", err, want)
	}
	if _, err := dir.CardPage(t.Context(), cardDriver, 0); !errors.Is(err, want) && !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("a card with no device = %v, want %v", err, want)
	}
}
