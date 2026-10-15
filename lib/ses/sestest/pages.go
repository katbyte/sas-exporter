// Package sestest makes up the pages an enclosure answers with, for tests: the ones that say what it holds, how each thing is doing, what each is
// called and the limits each is held to, laid out as a real enclosure lays them out.
package sestest

import (
	"encoding/binary"

	"github.com/katbyte/sas-exporter/lib/ses"
)

// Thing is one thing in a made-up enclosure: what the enclosure calls it, and the four bytes it gives for how it is doing.
type Thing struct {
	Name   string
	Status [4]byte
	// Limits is the four bytes the enclosure gives for the limits it holds the thing to, and nothing for a thing with none.
	Limits [4]byte
}

// Group is the things of one kind an enclosure holds.
type Group struct {
	Kind   ses.Kind
	Things []Thing
}

// Shelf is a made-up enclosure.
type Shelf struct {
	// Generation is the number an enclosure changes whenever what it holds does. Every page carries it.
	Generation uint32
	// Conditions is the byte that sums the enclosure up: 0x02 while something in it is critical, 0x04 non-critical, 0x01 unrecoverable, 0x08 for
	// something only worth knowing.
	Conditions byte
	Groups     []Group
	// Wiring is which connector each phy of each of the enclosure's expanders is wired to, for its page of more about things, and Counting how
	// that page numbers elements: 0 its SAS connectors alone, 1 every status in the page, 2 every element's own.
	Wiring   []Wiring
	Counting byte
	// Unindexed leaves out of each description which element it is for, as an older enclosure does: the descriptions are then in the order of the
	// elements that have one.
	Unindexed bool
}

// Wiring is one expander in a made-up enclosure: which of its SAS expander elements it is, its address, and for each of its phys the index of
// the connector it is wired to, or 0xff for none, counted as the shelf counts.
type Wiring struct {
	Expander   int
	Address    [8]byte
	Connectors []byte
}

// The statuses a test gives a thing. Each is the four bytes a real enclosure gives.
var (
	OK           = [4]byte{0x01, 0, 0, 0}
	NotInstalled = [4]byte{0x05, 0, 0, 0}
)

// Fan is the status of a fan turning that many times a minute, in the tens of turns an enclosure counts in.
func Fan(rpm int) [4]byte {
	status := [4]byte{0x01, 0, 0, 0x20}
	binary.BigEndian.PutUint16(status[1:], uint16(rpm/10)&0x07ff) //nolint:gosec // G115: a test's own small number, in the eleven bits there are for it

	return status
}

// Temperature is the status of a sensor at that many degrees.
func Temperature(celsius int) [4]byte {
	return [4]byte{0x01, 0, byte(celsius + 20), 0} //nolint:gosec // G115: a test's own small number, which the enclosure gives 20 above what it is
}

// Limits is the limits a temperature sensor is held to, in degrees, as the four bytes an enclosure gives: the highest it may read before it is
// critical and before it is a warning, then the lowest before a warning and before it is critical.
func Limits(highCritical, highWarning, lowWarning, lowCritical int) [4]byte {
	return [4]byte{byte(highCritical + 20), byte(highWarning + 20), byte(lowWarning + 20), byte(lowCritical + 20)} //nolint:gosec // G115: a test's own small numbers, which the enclosure gives 20 above what they are
}

// Hundredths is the status of a voltage or a current sensor reading that many hundredths of a volt or an amp: 1204 is 12.04.
func Hundredths(value int) [4]byte {
	status := [4]byte{0x01, 0, 0, 0}
	binary.BigEndian.PutUint16(status[2:], uint16(int16(value))) //nolint:gosec // G115: a test's own small number, signed as the enclosure gives it

	return status
}

// header is the eight bytes every page starts with: which page it is, a byte of its own, how long the rest is, and the generation.
func (s *Shelf) header(page, second byte, body []byte) []byte {
	out := make([]byte, 8, 8+len(body))
	out[0], out[1] = page, second
	binary.BigEndian.PutUint16(out[2:], uint16(len(body)+4)) //nolint:gosec // G115: a test's own page, a few hundred bytes
	binary.BigEndian.PutUint32(out[4:], s.Generation)

	return append(out, body...)
}

// Pages is the three pages the enclosure answers with.
func (s *Shelf) Pages() (configuration, status, names []byte) {
	// the enclosure describes itself in 36 bytes after the four that say how long that is: an identifier, its maker, its product and its revision
	described := append([]byte{0x11, 0, byte(len(s.Groups)), 36}, "\x50\x03\x04\x80\x00\x00\x00\x02SMC     SC846-P         100b"...) //nolint:gosec // G115: a test's own few kinds of thing

	var statuses, called []byte
	for _, group := range s.Groups {
		described = append(described, byte(group.Kind), byte(len(group.Things)), 0, 0) //nolint:gosec // G115: a test's own few things

		// one status and one name for the kind as a whole come before those of each thing
		statuses = append(statuses, 0, 0, 0, 0)
		called = append(called, 0, 0, 0, 0)

		for _, thing := range group.Things {
			statuses = append(statuses, thing.Status[:]...)
			called = append(called, 0, 0, 0, byte(len(thing.Name))) //nolint:gosec // G115: a test's own short name
			called = append(called, thing.Name...)
		}
	}

	return s.header(ses.PageConfiguration, 0, described), s.header(ses.PageStatus, s.Conditions, statuses), s.header(ses.PageNames, 0, called)
}

// Limits is the page of limits the enclosure answers with, laid out as its page of statuses is.
func (s *Shelf) Limits() []byte {
	var limits []byte
	for _, group := range s.Groups {
		limits = append(limits, 0, 0, 0, 0)

		for _, thing := range group.Things {
			limits = append(limits, thing.Limits[:]...)
		}
	}

	return s.header(ses.PageLimits, 0, limits)
}

// Additional is the page the enclosure says more of its things in: a description of each expander's phys, saying which element each is.
func (s *Shelf) Additional() []byte {
	var body []byte
	for _, wired := range s.Wiring {
		// where the expander is, counted as the shelf counts: among every status in the page, or among every element's own
		index, seen := 0, 0
		for _, group := range s.Groups {
			if s.Counting == 1 {
				index++
			}
			if group.Kind == ses.KindSASExpander && wired.Expander < seen+len(group.Things) {
				index += wired.Expander - seen

				break
			}
			if group.Kind == ses.KindSASExpander {
				seen += len(group.Things)
			}
			index += len(group.Things)
		}

		described := append([]byte{byte(len(wired.Connectors)), 0x40, 0, 0}, wired.Address[:]...) //nolint:gosec // G115: a test's own few phys
		for _, connector := range wired.Connectors {
			described = append(described, connector, 0xff)
		}
		if s.Unindexed {
			body = append(body, 0x06, byte(len(described))) //nolint:gosec // G115: a test's own short description
		} else {
			body = append(body, 0x16, byte(len(described)+2), s.Counting, byte(index)) //nolint:gosec // G115: a test's own short description and few elements
		}
		body = append(body, described...)
	}

	return s.header(ses.PageAdditional, 0, body)
}
