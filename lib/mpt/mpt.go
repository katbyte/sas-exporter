// Package mpt is the few words of a Broadcom SAS card's own language needed to ask it for one of its pages of settings and readings, and to read
// its temperature out of the answer. The layouts are the ones the kernel's mpt3sas driver uses for the same request.
//
// Nothing here talks to a card: it only makes the request and reads the reply. Sending it is package devices' business.
package mpt

import (
	"encoding/binary"
	"errors"
	"fmt"
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
	// pageTypeIOUnit is the kind of page that describes the card as a whole.
	pageTypeIOUnit = 0x00

	// where things are in a request and in a reply
	actionOffset   = 0
	functionOffset = 3
	statusOffset   = 14
	headerOffset   = 20

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

// Bytes is how long the page is.
func (h Header) Bytes() int {
	return int(h[1]) * 4
}

// IOUnit7 is the page a card keeps its own temperature in, at the version the kernel's driver knows it by.
var IOUnit7 = Header{0x05, 0, 7, pageTypeIOUnit}

// request is a request about one page, as the card expects it: mostly noughts, with what is wanted at the start and which page near the end.
func request(action byte, page Header) [RequestSize]byte {
	var frame [RequestSize]byte
	frame[actionOffset] = action
	frame[functionOffset] = functionConfig
	copy(frame[headerOffset:], page[:])

	return frame
}

// HeaderRequest asks a card whether it has a page and how long it is, which has to be known before the page itself can be asked for.
func HeaderRequest(page Header) [RequestSize]byte {
	return request(actionHeader, page)
}

// ReadRequest asks a card for a page as it is now, given the header the card answered a HeaderRequest with.
func ReadRequest(page Header) [RequestSize]byte {
	return request(actionRead, page)
}

// Reply reads a card's answer to either request: the header of the page, or what the card said instead. A card with no such page gives ErrNoPage.
func Reply(reply []byte) (Header, error) {
	if len(reply) < ReplySize {
		return Header{}, fmt.Errorf("the card's reply is %d bytes, too short to be one", len(reply))
	}
	if reply[functionOffset] != functionConfig {
		return Header{}, fmt.Errorf("the card answered a request for a page with a reply to function %#02x", reply[functionOffset])
	}

	switch status := binary.LittleEndian.Uint16(reply[statusOffset:]) & statusMask; status {
	case 0:
	case statusInvalidPage, statusInvalidType:
		return Header{}, ErrNoPage
	default:
		return Header{}, fmt.Errorf("the card refused a request for a page with status %#04x", status)
	}

	return Header(reply[headerOffset:]), nil
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
