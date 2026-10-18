// Package mpt is the few words of a Broadcom SAS card's own language needed to ask it for one of its pages of settings and readings, and to read
// its temperature out of the answer. The layouts are the ones the kernel's mpt3sas driver uses for the same request.
//
// Nothing here talks to a card: it only makes the request and reads the reply. Sending it is package devices' business.
package mpt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The sizes of a request for a page, up to where the driver adds how to hand the page back, and of the card's reply.
const (
	RequestSize = 28
	ReplySize   = 24
)

// The numbers a request is made of.
const (
	functionConfig = 0x04
	actionHeader   = 0x00
	actionRead     = 0x01
	// the kinds of page: one that describes the card as a whole, one of how it was made, and one of the kinds with a kind of their own
	pageTypeIOUnit        = 0x00
	pageTypeManufacturing = 0x09
	pageTypeExtended      = 0x0f
	// the kinds of extended page: about an expander the card has found, and about one of the card's own phys
	extendedExpander = 0x11
	extendedPhy      = 0x13
	// expanderPhyForm says an address is an expander's handle and one of its phys, the phy's number that far up
	expanderPhyForm  = 0x10000000
	expanderPhyShift = 16

	// where things are in a request and in a reply
	actionOffset         = 0
	functionOffset       = 3
	extendedLengthOffset = 4
	extendedTypeOffset   = 6
	statusOffset         = 14
	headerOffset         = 20
	addressOffset        = 24

	// statusMask leaves out the bit that only says the card has more to say in its log.
	statusMask = 0x7fff
)

// What a card says when it has no such page: not a fault, only a card that does not keep that reading.
const (
	statusInvalidType = 0x0021
	statusInvalidPage = 0x0022
)

// ErrNoPage is a card that has no such page.
var ErrNoPage = errors.New("the card has no such page")

// Header is the four bytes that name a page and say how long it is: its version, its length in units of four bytes, its number and its type.
type Header [4]byte

// Page is one page of a card's: its kind, its number and the version the kernel's driver knows it by, its own kind when it is an extended page,
// and which one of its kind it is, a phy or an expander, by an address.
type Page struct {
	Type, Number, Version, Extended byte
	Address                         uint32
}

func (p Page) String() string {
	return fmt.Sprintf("page %d of kind %#02x/%#02x at %#08x", p.Number, p.Type, p.Extended, p.Address)
}

// The pages a card is asked for, at the versions the kernel's driver knows them by.
var (
	// IOUnit7 is the page a card keeps its own temperature in, and how fast and on what power it runs.
	IOUnit7 = Page{Type: pageTypeIOUnit, Number: 7, Version: 0x05}
	// Manufacturing7 is the page that says which connector each of the card's phys is wired to.
	Manufacturing7 = Page{Type: pageTypeManufacturing, Number: 7, Version: 0x01}
)

// PhyPage is the page about one of the card's own phys, which says how many times its link has changed.
func PhyPage(phy int) Page {
	return Page{Type: pageTypeExtended, Extended: extendedPhy, Version: 0x03, Address: uint32(phy)} //nolint:gosec // G115: a card has a few dozen phys
}

// NextExpanderPage is the page about the expander the card lists after the one with that handle: the first, after 0xffff. A card has no such page
// once there is none after.
func NextExpanderPage(handle uint16) Page {
	return Page{Type: pageTypeExtended, Extended: extendedExpander, Version: 0x06, Address: uint32(handle)}
}

// ExpanderPhyPage is the page about one phy of an expander, by the expander's handle, which says how many times its link has changed.
func ExpanderPhyPage(handle uint16, phy int) Page {
	return Page{Type: pageTypeExtended, Extended: extendedExpander, Number: 1, Version: 0x02, Address: expanderPhyForm | uint32(phy)<<expanderPhyShift | uint32(handle)} //nolint:gosec // G115: an expander has a few dozen phys
}

// Answer is what a card says when asked whether it has a page: the page's header, and an extended page's own kind and length, all of which the
// read of it has to repeat.
type Answer struct {
	Header Header
	// ExtendedLength is an extended page's length in units of four bytes, and ExtendedType its kind; both are 0 for a page that is not one.
	ExtendedLength uint16
	ExtendedType   byte
}

// Bytes is how long the page is.
func (a Answer) Bytes() int {
	if a.Header[1] != 0 {
		return int(a.Header[1]) * 4
	}

	return int(a.ExtendedLength) * 4
}

// HeaderRequest asks a card whether it has a page and how long it is, which has to be known before the page itself can be asked for. It is the
// request the kernel's driver makes: what is wanted at the start, and which page near the end.
func HeaderRequest(page Page) [RequestSize]byte {
	var frame [RequestSize]byte
	frame[actionOffset] = actionHeader
	frame[functionOffset] = functionConfig
	frame[extendedTypeOffset] = page.Extended
	copy(frame[headerOffset:], []byte{page.Version, 0, page.Number, page.Type})

	return frame
}

// ReadRequest asks a card for a page as it is now, given what the card answered a HeaderRequest for it with.
func ReadRequest(page Page, answer Answer) [RequestSize]byte {
	var frame [RequestSize]byte
	frame[actionOffset] = actionRead
	frame[functionOffset] = functionConfig
	binary.LittleEndian.PutUint16(frame[extendedLengthOffset:], answer.ExtendedLength)
	frame[extendedTypeOffset] = answer.ExtendedType
	copy(frame[headerOffset:], answer.Header[:])
	binary.LittleEndian.PutUint32(frame[addressOffset:], page.Address)

	return frame
}

// Reply reads a card's answer to either request: what it says of the page, or what it said instead. A card with no such page gives ErrNoPage.
func Reply(reply []byte) (Answer, error) {
	if len(reply) < ReplySize {
		return Answer{}, fmt.Errorf("the card's reply is %d bytes, too short to be one", len(reply))
	}
	if reply[functionOffset] != functionConfig {
		return Answer{}, fmt.Errorf("the card answered a request for a page with a reply to function %#02x", reply[functionOffset])
	}

	switch status := binary.LittleEndian.Uint16(reply[statusOffset:]) & statusMask; status {
	case 0:
	case statusInvalidPage, statusInvalidType:
		return Answer{}, ErrNoPage
	default:
		return Answer{}, fmt.Errorf("the card refused a request for a page with status %#04x", status)
	}

	return Answer{Header: Header(reply[headerOffset:]), ExtendedLength: binary.LittleEndian.Uint16(reply[extendedLengthOffset:]), ExtendedType: reply[extendedTypeOffset]}, nil
}

// Where a card's temperatures are in its page, and how it says which scale each is on.
const (
	chipOffset       = 0x10
	chipUnitsOffset  = 0x12
	boardOffset      = 0x14
	boardUnitsOffset = 0x16

	unitsFahrenheit = 1
	unitsCelsius    = 2
)

// Temperature is one temperature a card takes of itself, in degrees Celsius.
type Temperature struct {
	// Board says it is of the card around the processor, and not of the processor itself.
	Board   bool
	Celsius float64
}

// Temperatures reads the temperatures out of a card's IOUnit7 page: its processor's, and the board's where the card takes that too. A card that
// says it has no sensor gives none, and so does a page from firmware too old to have the field.
func Temperatures(page []byte) []Temperature {
	var out []Temperature
	for _, sensor := range []struct {
		at, units int
		board     bool
	}{{chipOffset, chipUnitsOffset, false}, {boardOffset, boardUnitsOffset, true}} {
		if len(page) <= sensor.units {
			continue
		}

		degrees := float64(int16(binary.LittleEndian.Uint16(page[sensor.at:]))) //nolint:gosec // G115: the two bytes are a signed number, which is what the conversion says
		switch page[sensor.units] {
		case unitsCelsius:
			out = append(out, Temperature{Board: sensor.board, Celsius: degrees})
		case unitsFahrenheit:
			out = append(out, Temperature{Board: sensor.board, Celsius: (degrees - 32) * 5 / 9})
		}
	}

	return out
}

// Where a card says which power mode it is in and how fast its processor runs, and the modes it can be in.
const (
	powerModeOffset = 0x04
	powerModeMask   = 0x07
	speedOffset     = 0x13
)

// The power modes a card can be in, as the page's words.
const (
	PowerFull    = "full"
	PowerReduced = "reduced"
	PowerStandby = "standby"
)

// PowerModes is every power mode a card can say it is in.
var PowerModes = []string{PowerFull, PowerReduced, PowerStandby}

// Speed is how fast a card's processor runs as a share of its full speed, from its IOUnit7 page: 1, or a half, a quarter or an eighth for a card
// that has slowed itself. False for a page that does not say.
func Speed(page []byte) (float64, bool) {
	if len(page) <= speedOffset {
		return 0, false
	}

	share, known := map[byte]float64{0x01: 1, 0x02: 0.5, 0x04: 0.25, 0x08: 0.125}[page[speedOffset]]

	return share, known
}

// PowerMode is the power a card runs on, from its IOUnit7 page: full, reduced or standby. Empty for a page that does not say, as a card that does
// not manage its power does not.
func PowerMode(page []byte) string {
	if len(page) <= powerModeOffset {
		return ""
	}

	return map[byte]string{0x04: PowerFull, 0x05: PowerReduced, 0x06: PowerStandby}[page[powerModeOffset]&powerModeMask]
}

// Connector is the connector on a card one of its phys is wired to, by the name printed beside it: C0.0.
type Connector struct {
	Phy  int
	Name string
}

// Where a card says which connector each of its phys is wired to: how many phys, then a description of each, of which the name and whether the phy
// is wired to any connector at all are read.
const (
	connectorPhysOffset = 0x20
	connectorsOffset    = 0x24
	connectorSize       = 0x1c
	connectorNameOffset = 0x04
	connectorNameSize   = 16
	connectorWhere      = 0x14
	// a phy wired to no connector, or to one the card does not have fitted
	connectorNotPresent   = 0x20
	connectorNotConnected = 0x80
)

// Connectors reads which connector each of a card's phys is wired to out of its Manufacturing7 page. A phy wired to none is left out, and so is one
// the card gives no name for.
func Connectors(page []byte) []Connector {
	if len(page) <= connectorPhysOffset {
		return nil
	}

	var out []Connector
	for phy := range int(page[connectorPhysOffset]) {
		at := connectorsOffset + phy*connectorSize
		if at+connectorSize > len(page) {
			break
		}

		name := strings.Trim(string(page[at+connectorNameOffset:at+connectorNameOffset+connectorNameSize]), " \x00")
		if name == "" || page[at+connectorWhere]&(connectorNotPresent|connectorNotConnected) != 0 {
			continue
		}

		out = append(out, Connector{Phy: phy, Name: strings.Map(func(r rune) rune {
			if r < ' ' || r > '~' {
				return -1
			}

			return r
		}, name)})
	}

	return out
}

// Where a card says how many times a phy's link has changed: in its page about one of its own phys, and in its page about one phy of an expander,
// which also says which phy it is. The card keeps eight bits of each, so a count goes round to 0 after 255.
const (
	phyChangesOffset         = 0x16
	expanderPhyChangesOffset = 0x1e
	expanderPhyNumberOffset  = 0x20
)

// PhyChanges is how many times the link of one of the card's own phys has changed, from the card's page about it.
func PhyChanges(page []byte) (int, bool) {
	if len(page) <= phyChangesOffset {
		return 0, false
	}

	return int(page[phyChangesOffset]), true
}

// ExpanderPhyChanges is which phy of an expander the card's page is about, and how many times its link has changed.
func ExpanderPhyChanges(page []byte) (phy, changes int, ok bool) {
	if len(page) <= expanderPhyNumberOffset {
		return 0, 0, false
	}

	return int(page[expanderPhyNumberOffset]), int(page[expanderPhyChangesOffset]), true
}

// Expander is what a card says of an expander it has found: the handle it knows it by, its SAS address as sixteen small hex digits, how many phys
// it has, and how many times anything about it has changed, which the card keeps sixteen bits of.
type Expander struct {
	Handle  uint16
	Address string
	Phys    int
	Changes int
}

// Where a card's page about an expander says each of those.
const (
	expanderAddressOffset = 0x0c
	expanderHandleOffset  = 0x18
	expanderChangesOffset = 0x1c
	expanderPhysOffset    = 0x20
)

// ExpanderOf reads what a card says of an expander out of its page about it.
func ExpanderOf(page []byte) (Expander, bool) {
	if len(page) <= expanderPhysOffset {
		return Expander{}, false
	}

	return Expander{
		Handle:  binary.LittleEndian.Uint16(page[expanderHandleOffset:]),
		Address: fmt.Sprintf("%016x", binary.LittleEndian.Uint64(page[expanderAddressOffset:])),
		Phys:    int(page[expanderPhysOffset]),
		Changes: int(binary.LittleEndian.Uint16(page[expanderChangesOffset:])),
	}, true
}
