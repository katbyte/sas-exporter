package sysfstest

import (
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nas is what the kernel said of the SAS cards in a real machine, with its addresses and serial numbers swapped for made-up ones: see the top of
// the file.
//
//go:embed testdata/nas.txt
var nas string

// What a real machine's capture holds, for tests to count against.
const (
	NASCards      = 2
	NASExpanders  = 4
	NASPhys       = 214
	NASDisks      = 58
	NASEnclosures = 4
	NASSlots      = 80
	// NASRefused is the phys whose error counts the expanders would not give: eight on each card's first expander, with nothing wired to them.
	NASRefused = 16
	// What its enclosures say they hold beside slots. The two behind the second card share a chassis and each reports its seven fans and two
	// power supplies; the two behind the first report neither.
	NASFans         = 14
	NASSupplies     = 4
	NASTemperatures = 10
	NASVoltages     = 24
)

// Capture is what a capture holds beside the files it lays out.
type Capture struct {
	// Refused is the files the kernel would not read when the capture was made, which are left out.
	Refused []string
	// Pages is what each enclosure answered when asked about itself, by the device it was asked through and then by the page's number. Empty for
	// a capture made without root.
	Pages map[string]map[byte][]byte
}

// Diagnostic answers for an enclosure with the page it answered with when the capture was made, as the device it is asked through would, and
// says there is no such device for one the capture holds no pages of.
func (c *Capture) Diagnostic(_ context.Context, device string, page byte) ([]byte, error) {
	if answered, kept := c.Pages[device][page]; kept {
		return answered, nil
	}

	return nil, fmt.Errorf("%s: %w", device, fs.ErrNotExist)
}

// NAS lays a real machine's /sys out under a directory of the test's own, and gives back where it is and what else its capture holds.
func NAS(t *testing.T) (root string, c Capture) {
	t.Helper()

	root = tempRoot(t)
	c, err := Lay(root, nas)
	if err != nil {
		t.Fatal(err)
	}

	return root, c
}

// Lay lays a capture out as directories, files and links under root, the way the kernel's /sys had them: a capture is what .scripts/capture.sh
// prints. A file the kernel would not read when the capture was made is left out and named in what is given back, and so are the pages the
// enclosures answered with, which are not files.
func Lay(root, capture string) (c Capture, err error) {
	c.Pages = map[string]map[byte][]byte{}

	for line := range strings.Lines(capture) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		kind, rest, found := strings.Cut(line, "\t")
		if !found {
			return Capture{}, fmt.Errorf("a line of the capture has no path: %q", line)
		}
		rel, value, _ := strings.Cut(rest, "\t")
		path := filepath.Join(root, filepath.FromSlash(rel))

		switch kind {
		case "!":
			c.Refused = append(c.Refused, path)
		case "s":
			if err := c.page(rel, value); err != nil {
				return Capture{}, err
			}
		default:
			if err := put(kind, path, value); err != nil {
				return Capture{}, err
			}
		}
	}

	return c, nil
}

// page keeps one page an enclosure answered with, which a capture names by the device it was asked through and the page's number: sg26/02.
func (c *Capture) page(named, value string) error {
	device, number, found := strings.Cut(named, "/")
	which, err := hex.DecodeString(number)
	if !found || err != nil || len(which) != 1 {
		return fmt.Errorf("a page in the capture is named %q, which is not a device and a page", named)
	}

	raw, err := hex.DecodeString(value)
	if err != nil {
		return fmt.Errorf("the page %s is not hex in the capture: %w", named, err)
	}

	if c.Pages[device] == nil {
		c.Pages[device] = map[byte][]byte{}
	}
	c.Pages[device][which[0]] = raw

	return nil
}

// put makes one thing a capture names: a directory, a link, or a file holding what it held.
func put(kind, path, value string) error {
	if kind == "d" {
		return os.MkdirAll(path, dirMode)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}

	switch kind {
	case "l":
		return os.Symlink(value, path)
	case "f":
		return os.WriteFile(path, []byte(value+"\n"), fileMode)
	case "-":
		return os.WriteFile(path, nil, fileMode)
	case "x":
		raw, err := hex.DecodeString(value)
		if err != nil {
			return fmt.Errorf("%s is not hex in the capture: %w", path, err)
		}

		return os.WriteFile(path, raw, fileMode)
	}

	return fmt.Errorf("a line of the capture is of kind %q, and a capture has only d, l, f, x, s, - and ! lines", kind)
}
