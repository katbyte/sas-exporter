package mpt_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/katbyte/sas-exporter/lib/mpt"
)

// reply is a card's answer with that status, naming a page of that length.
func reply(status uint16, length byte) []byte {
	out := make([]byte, mpt.ReplySize)
	out[3] = 0x04
	binary.LittleEndian.PutUint16(out[14:], status)
	copy(out[20:], []byte{0x05, length, 7, 0x00})

	return out
}

// page is a card's IO unit page 7 with those temperatures and the scales they are on.
func page(chip uint16, chipUnits byte, board uint16, boardUnits byte) []byte {
	out := make([]byte, 0x28)
	binary.LittleEndian.PutUint16(out[0x10:], chip)
	binary.LittleEndian.PutUint16(out[0x14:], board)
	out[0x12], out[0x16] = chipUnits, boardUnits

	return out
}

// A request is checked byte for byte, since the card's firmware is what reads it: the same 28 bytes the kernel's own driver sends when it asks a
// card for a page, with what is wanted first and which page at the end.
func TestTheRequestsAreTheOnesTheDriverSends(t *testing.T) {
	t.Parallel()

	want := [mpt.RequestSize]byte{0: 0x00, 3: 0x04, 20: 0x05, 21: 0x00, 22: 0x07, 23: 0x00}
	if got := mpt.HeaderRequest(mpt.IOUnit7); got != want {
		t.Errorf("the request for the header is\n% x\nwant\n% x", got, want)
	}

	// the page is then asked for with the header the card gave, which is where its length comes from
	header, err := mpt.Reply(reply(0, 10))
	if err != nil || header != (mpt.Header{0x05, 10, 7, 0x00}) || header.Bytes() != 40 {
		t.Fatalf("the header read back is %v, %d bytes long (%v); want version 5, 40 bytes, page 7", header, header.Bytes(), err)
	}

	want = [mpt.RequestSize]byte{0: 0x01, 3: 0x04, 20: 0x05, 21: 10, 22: 0x07, 23: 0x00}
	if got := mpt.ReadRequest(header); got != want {
		t.Errorf("the request for the page is\n% x\nwant\n% x", got, want)
	}
}

// A card that has no such page says so, which is a card without that reading and not a failure. Anything else it says instead of the page is
// one, and so is a reply that is not a reply to this.
func TestWhatACardSaysInsteadOfAPage(t *testing.T) {
	t.Parallel()

	for _, status := range []uint16{0x0022, 0x0021, 0x8022} {
		if _, err := mpt.Reply(reply(status, 0)); !errors.Is(err, mpt.ErrNoPage) {
			t.Errorf("status %#04x = %v, want it read as no such page", status, err)
		}
	}

	for _, status := range []uint16{0x0001, 0x0002, 0x0020, 0x0006} {
		if _, err := mpt.Reply(reply(status, 0)); err == nil || errors.Is(err, mpt.ErrNoPage) {
			t.Errorf("status %#04x = %v, want a failure", status, err)
		}
	}

	// the top bit only says the card has put something in its log
	if _, err := mpt.Reply(reply(0x8000, 10)); err != nil {
		t.Errorf("a reply that is fine with the log bit set = %v, want it taken", err)
	}

	other := reply(0, 10)
	other[3] = 0x03
	if _, err := mpt.Reply(other); err == nil {
		t.Error("a reply to some other request was taken")
	}
	if _, err := mpt.Reply(make([]byte, 10)); err == nil {
		t.Error("a reply cut short was taken")
	}
}

// A card gives its processor's temperature and sometimes its board's, each with the scale it is on. One it says it has no sensor for is left out
// rather than given as 0, and one in Fahrenheit is turned into Celsius.
func TestACardsTemperatures(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		page []byte
		want []mpt.Temperature
	}{
		"the processor alone":         {page(53, 2, 0, 0), []mpt.Temperature{{Celsius: 53}}},
		"the processor and board":     {page(53, 2, 41, 2), []mpt.Temperature{{Celsius: 53}, {Board: true, Celsius: 41}}},
		"in Fahrenheit":               {page(212, 1, 32, 1), []mpt.Temperature{{Celsius: 100}, {Board: true, Celsius: 0}}},
		"below freezing":              {page(0xfff6, 2, 0, 0), []mpt.Temperature{{Celsius: -10}}},
		"no sensor":                   {page(0, 0, 0, 0), nil},
		"a scale nobody has heard of": {page(53, 3, 0, 0), nil},
		"a page from old firmware":    {page(53, 2, 41, 2)[:0x14], []mpt.Temperature{{Celsius: 53}}},
		"a page too short for any":    {make([]byte, 8), nil},
		"nothing":                     {nil, nil},
	} {
		got := mpt.Temperatures(c.page)
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
	}
}

// A card says how fast its processor runs as one of four shares of its full speed, and which power mode it is in. Anything else, or a page too
// short to say, is not known.
func TestACardsSpeedAndPower(t *testing.T) {
	t.Parallel()

	for code, want := range map[byte]float64{0x01: 1, 0x02: 0.5, 0x04: 0.25, 0x08: 0.125} {
		p := page(53, 2, 0, 0)
		p[0x13] = code
		if got, known := mpt.Speed(p); !known || got != want {
			t.Errorf("speed %#02x is %v (known %t), want %v", code, got, known, want)
		}
	}
	if _, known := mpt.Speed(page(53, 2, 0, 0)); known {
		t.Error("a speed of 0 is known")
	}
	if _, known := mpt.Speed(make([]byte, 0x13)); known {
		t.Error("a page too short to say has a speed")
	}

	// the top bits say who set the mode, and are not the mode
	for code, want := range map[byte]string{0x04: mpt.PowerFull, 0x45: mpt.PowerReduced, 0x86: mpt.PowerStandby, 0x01: "", 0x00: ""} {
		p := page(53, 2, 0, 0)
		p[0x04] = code
		if got := mpt.PowerMode(p); got != want {
			t.Errorf("power mode %#02x is %q, want %q", code, got, want)
		}
	}
	if got := mpt.PowerMode(make([]byte, 4)); got != "" {
		t.Errorf("a page too short to say is in power mode %q", got)
	}
}
