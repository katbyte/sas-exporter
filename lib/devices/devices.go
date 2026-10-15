// Package devices asks the hardware itself for what the kernel does not keep in /sys: an enclosure for the pages that describe what is in it, and
// a card for the page its own temperature is in. Both mean opening a device file, which takes leave an ordinary user does not have, and both
// only ever read.
//
// It is the one part of sas-exporter that can only be run for real on a machine with the hardware in it.
package devices

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Dir is the directory the machine's device files are in: /dev.
type Dir string

// The SCSI command that asks an enclosure for one page of its diagnostics, RECEIVE DIAGNOSTIC RESULTS, and the most it can be asked to send back.
const (
	receiveDiagnostic = 0x1c
	// pageCodeValid says the command names the page it wants.
	pageCodeValid = 0x01
	// allocation is how much room the enclosure is told there is: the most the command can say, less a little to keep it a multiple of four.
	allocation = 0xfffc
	// diagnosticHeader is the bytes every page starts with, the last two of which say how long the rest is.
	diagnosticHeader = 4
)

// diagnosticCommand is the six bytes that ask for one page.
func diagnosticCommand(page byte) [6]byte {
	return [6]byte{receiveDiagnostic, pageCodeValid, page, allocation >> 8, allocation & 0xff, 0}
}

// diagnosticPage is the page in what an enclosure sent back: as much as the page says it is long, and no more than arrived.
func diagnosticPage(page byte, sent []byte) ([]byte, error) {
	if len(sent) < diagnosticHeader {
		return nil, fmt.Errorf("the enclosure sent %d bytes for page %#02x, too few to be a page", len(sent), page)
	}

	if length := diagnosticHeader + int(binary.BigEndian.Uint16(sent[2:])); length < len(sent) {
		sent = sent[:length]
	}

	return sent, nil
}

// plain says a name is one file's and nothing more: the name of a device comes from the kernel, and is still never let lead out of the directory.
func plain(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// The driver whose cards are asked for their own temperature, and the device file it is asked through.
const (
	cardDriver = "mpt3sas"
	cardDevice = "mpt3ctl"
)
