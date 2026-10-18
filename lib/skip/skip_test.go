package skip_test

import (
	"strings"
	"testing"

	"github.com/katbyte/sas-exporter/lib/skip"
)

// A list of parts is read whatever its spacing and case, and leaving out a part leaves out what is within it. Nothing named leaves nothing out.
func TestWhatIsLeftOut(t *testing.T) {
	t.Parallel()

	s, err := skip.Parse(" Phys , slots,,")
	if err != nil {
		t.Fatal(err)
	}
	for part, want := range map[skip.Part]bool{skip.Phys: true, skip.PhyErrors: true, skip.LinkChanges: true, skip.Slots: true, skip.Enclosures: false, skip.EnclosureSensors: false, skip.Devices: false} {
		if s.Has(part) != want {
			t.Errorf("%s left out = %t, want %t", part, s.Has(part), want)
		}
	}
	if s.String() != "phys, slots" {
		t.Errorf("the parts left out read %q, want phys, slots", s.String())
	}

	none, err := skip.Parse("")
	if err != nil || len(none) != 0 || none.Has(skip.Cards) {
		t.Errorf("nothing named = %v (%v), want nothing left out", none, err)
	}
}

// Asking about only some parts leaves out every other, but keeps what is within those named and what they are within: the lanes, for their link
// changes, and the cards' own questions too, as the link changes are asked of the cards.
func TestOnlyThePartsNamed(t *testing.T) {
	t.Parallel()

	s, err := skip.AllBut("link-changes, Slots")
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "poll, cards, expanders, phy-errors, devices, enclosure-sensors" {
		t.Errorf("asking about link changes and slots alone leaves out %q", s.String())
	}
	for _, part := range []skip.Part{skip.LinkChanges, skip.Phys, skip.CardSensors, skip.Slots, skip.Enclosures} {
		if s.Has(part) {
			t.Errorf("%s is left out, though it is named or holds one that is", part)
		}
	}

	s, err = skip.AllBut("enclosures")
	if err != nil {
		t.Fatal(err)
	}
	if s.Has(skip.Slots) || s.Has(skip.EnclosureSensors) || !s.Has(skip.Cards) || !s.Has(skip.CardSensors) {
		t.Errorf("asking about the enclosures alone leaves out %q, want everything but them, their slots and their sensors", s.String())
	}

	// what is asked of a card comes only when named, so naming the lanes does not start a hundred questions of each card
	s, err = skip.AllBut("phys")
	if err != nil {
		t.Fatal(err)
	}
	if s.Has(skip.PhyErrors) || !s.Has(skip.LinkChanges) || !s.Has(skip.CardSensors) {
		t.Errorf("asking about the lanes alone leaves out %q, want their errors kept and what is asked of the cards left out", s.String())
	}

	none, err := skip.AllBut("")
	if err != nil || len(none) != 0 {
		t.Errorf("nothing named = %v (%v), want nothing left out", none, err)
	}

	if _, err := skip.AllBut("pools"); err == nil || !strings.Contains(err.Error(), `"pools"`) {
		t.Errorf("asking about pools = %v, want it refused", err)
	}
}

// A name that is no part is refused, with the names there are, so a misspelling cannot leave nothing out unnoticed.
func TestANameThatIsNoPart(t *testing.T) {
	t.Parallel()

	if _, err := skip.Parse("slots,fans"); err == nil || !strings.Contains(err.Error(), `"fans"`) || !strings.Contains(err.Error(), "enclosure-sensors") {
		t.Errorf("a list with fans in it = %v, want it refused, naming fans and the parts there are", err)
	}
}
