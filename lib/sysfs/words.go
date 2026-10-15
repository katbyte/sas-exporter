package sysfs

import (
	"strconv"
	"strings"

	"github.com/katbyte/sas-exporter/lib/poll"
)

// gigabitWord ends the kernel's word for a link speed, "12.0 Gbit".
const gigabitWord = " Gbit"

// The kernel's words for a link that has no speed, each as the state the page gives it.
var linkWords = map[string]poll.LinkState{
	"Unknown":          poll.LinkDown,
	"Phy disabled":     poll.LinkDisabled,
	"Link Rate failed": poll.LinkFailed,
	"Spinup hold":      poll.LinkSpinupHold,
}

// link is what the kernel's word for a phy's speed says of its link: up at so many gigabits a second, or one of the states it has no speed in. A
// word never seen before is other, at no speed, so a kernel that learns a new one shows up on the page rather than as a link that is down.
func link(word string) (state poll.LinkState, rate float64) {
	if state, known := linkWords[word]; known {
		return state, 0
	}

	if speed, found := strings.CutSuffix(word, gigabitWord); found {
		if gigabits, err := strconv.ParseFloat(speed, 64); err == nil && gigabits > 0 {
			return poll.LinkUp, gigabits
		}
	}

	return poll.LinkOther, 0
}

// word is one of the kernel's words as the page writes it: small letters, with an underscore where it has a space or a dash. "not installed" is
// not_installed, "transport-offline" transport_offline and "OK" ok.
func word(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(s))
}

// address is one of the kernel's addresses, 0x500304801eb5a03f, as sixteen small hex digits with nothing before them.
func address(s string) string {
	s = strings.ToLower(s)
	if rest, found := strings.CutPrefix(s, "0x"); found {
		return rest
	}

	return s
}

// wwn is a device's world wide name as hex with nothing before it, from the kernel's own identifier for the device: naa.5000cca2912345ab. The
// kernel names a device with no such name some other way, a maker's and a model's name strung together say, and that is not one.
func wwn(id string) string {
	id = strings.ToLower(id)
	for _, prefix := range []string{"naa.", "eui."} {
		if rest, found := strings.CutPrefix(id, prefix); found {
			return rest
		}
	}

	return ""
}

// The page of a device's own description that holds its serial number, as the kernel hands it over: four bytes saying what it is and how long, and
// then the number.
const (
	serialPage       = 0x80
	serialPageHeader = 4
)

// serial is the serial number in that page, less the spaces it is padded with, and nothing when the page is not the one expected.
func serial(page []byte) string {
	if len(page) < serialPageHeader || page[1] != serialPage {
		return ""
	}

	body := page[serialPageHeader:]
	if length := int(page[2])<<8 | int(page[3]); length < len(body) {
		body = body[:length]
	}

	return clean(string(body))
}

// clean is what the kernel wrote less the padding around it, and less anything a metrics store would refuse as a label.
func clean(s string) string {
	return strings.Trim(strings.ToValidUTF8(s, ""), " \t\r\n\x00")
}

// transfers is the speed of each lane of a card's own link to the machine, from the kernel's words for it: "16.0 GT/s PCIe" is 16 gigatransfers a
// second. A speed the kernel does not know is 0.
func transfers(said string) float64 {
	fields := strings.Fields(said)
	if len(fields) == 0 {
		return 0
	}

	speed, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || speed < 0 {
		return 0
	}

	return speed
}
