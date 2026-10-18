package mpt_test

import (
	"encoding/binary"
	"errors"
	"slices"
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
	answer, err := mpt.Reply(reply(0, 10))
	if err != nil || answer.Header != (mpt.Header{0x05, 10, 7, 0x00}) || answer.Bytes() != 40 {
		t.Fatalf("the header read back is %v, %d bytes long (%v); want version 5, 40 bytes, page 7", answer.Header, answer.Bytes(), err)
	}

	want = [mpt.RequestSize]byte{0: 0x01, 3: 0x04, 20: 0x05, 21: 10, 22: 0x07, 23: 0x00}
	if got := mpt.ReadRequest(mpt.IOUnit7, answer); got != want {
		t.Errorf("the request for the page is\n% x\nwant\n% x", got, want)
	}
}

// An extended page is asked for as the driver asks for one: its own kind beside the header, which says only that it is extended, and its length
// in a field of its own. The read repeats both and names which phy of which expander it is about.
func TestTheRequestsForAnExtendedPage(t *testing.T) {
	t.Parallel()

	page := mpt.ExpanderPhyPage(0x0009, 12)
	want := [mpt.RequestSize]byte{0: 0x00, 3: 0x04, 6: 0x11, 20: 0x02, 21: 0, 22: 1, 23: 0x0f}
	if got := mpt.HeaderRequest(page); got != want {
		t.Errorf("the request for the header is\n% x\nwant\n% x", got, want)
	}

	// an extended page's length is in the reply's own field for it, and its header says nothing of it
	got := reply(0, 0)
	got[4], got[6] = 12, 0x11
	copy(got[20:], []byte{0x02, 0, 1, 0x0f})
	answer, err := mpt.Reply(got)
	if err != nil || answer.Bytes() != 48 || answer.ExtendedType != 0x11 {
		t.Fatalf("the answer read back is %+v, %d bytes long (%v); want an expander page of 48 bytes", answer, answer.Bytes(), err)
	}

	want = [mpt.RequestSize]byte{0: 0x01, 3: 0x04, 4: 12, 6: 0x11, 20: 0x02, 21: 0, 22: 1, 23: 0x0f, 24: 0x09, 25: 0x00, 26: 12, 27: 0x10}
	if got := mpt.ReadRequest(page, answer); got != want {
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

// A card names the connector each of its phys is wired to, as printed beside it. A phy wired to none, or given no name, is left out, and so is
// what a page too short to hold says.
func TestWhichConnectorEachOfACardsPhysIsWiredTo(t *testing.T) {
	t.Parallel()

	page := make([]byte, 0x24+4*0x1c)
	page[0x20] = 5 // one more than the page holds
	for phy, c := range []struct {
		name  string
		where byte
	}{{"C0.0", 0x02}, {"C0.1", 0x02}, {"", 0x02}, {"C9.9", 0x80}} {
		copy(page[0x24+phy*0x1c+4:], c.name)
		page[0x24+phy*0x1c+0x14] = c.where
	}

	want := []mpt.Connector{{Phy: 0, Name: "C0.0"}, {Phy: 1, Name: "C0.1"}}
	if got := mpt.Connectors(page); !slices.Equal(got, want) {
		t.Errorf("connectors = %+v, want %+v", got, want)
	}
	if got := mpt.Connectors(page[:0x20]); got != nil {
		t.Errorf("a page too short to say has connectors %+v", got)
	}
}

// A card counts how many times each phy's link has changed, its own and every expander's, and says what it has found of each expander.
func TestHowOftenALinkHasChanged(t *testing.T) {
	t.Parallel()

	own := make([]byte, 0x24)
	own[0x16] = 3
	if changes, ok := mpt.PhyChanges(own); !ok || changes != 3 {
		t.Errorf("the card's own phy changed %d times (%t), want 3", changes, ok)
	}
	if _, ok := mpt.PhyChanges(own[:0x16]); ok {
		t.Error("a page too short to say has a count")
	}

	theirs := make([]byte, 0x2c)
	theirs[0x1e], theirs[0x20] = 255, 12
	if phy, changes, ok := mpt.ExpanderPhyChanges(theirs); !ok || phy != 12 || changes != 255 {
		t.Errorf("the expander's phy %d changed %d times (%t), want phy 12 at 255", phy, changes, ok)
	}

	expander := make([]byte, 0x30)
	binary.LittleEndian.PutUint64(expander[0x0c:], 0x500304801eb5a03f)
	binary.LittleEndian.PutUint16(expander[0x18:], 0x0009)
	binary.LittleEndian.PutUint16(expander[0x1c:], 300)
	expander[0x20] = 51
	if got, ok := mpt.ExpanderOf(expander); !ok || got != (mpt.Expander{Handle: 9, Address: "500304801eb5a03f", Phys: 51, Changes: 300}) {
		t.Errorf("the expander = %+v (%t), want handle 9, its address, 51 phys and 300 changes", got, ok)
	}
}
