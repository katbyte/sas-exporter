package ses_test

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/katbyte/sas-exporter/lib/ses"
)

// areca is the pages of a real enclosure, by the page's own number: an Areca SAS-3 expander, as the sg_ses tool saved them. It is in the form that
// tool writes, pages of hex one after another with a line of words above each.
func areca(t *testing.T) map[byte][]byte {
	t.Helper()

	raw, err := os.ReadFile("testdata/areca.hex")
	if err != nil {
		t.Fatal(err)
	}

	pages := map[byte][]byte{}
	var page []byte
	keep := func() {
		// the first page of the file lists the pages there are, under the number 0, and the names of two later ones say they are pages too
		if len(page) > 0 && pages[page[0]] == nil {
			pages[page[0]] = page
		}
		page = nil
	}

	for line := range strings.Lines(string(raw)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			keep()
			continue
		}

		bytes, err := hex.DecodeString(strings.ReplaceAll(line, " ", ""))
		if err != nil {
			t.Fatalf("a line of the file is not hex: %q", line)
		}
		page = append(page, bytes...)
	}
	keep()

	return pages
}

// A real enclosure's pages, read against what the tool that saved them prints for the same bytes: 41 things of nine kinds, one disk in 24 slots,
// one fan turning of five, two temperatures, two voltages, and the enclosure saying something in it is critical.
func TestARealEnclosure(t *testing.T) {
	t.Parallel()

	pages := areca(t)
	e, err := ses.Parse(pages[ses.PageConfiguration], pages[ses.PageStatus], pages[ses.PageNames])
	if err != nil {
		t.Fatal(err)
	}

	if !e.Critical || e.NonCritical || e.Unrecoverable || e.Info {
		t.Errorf("the enclosure sums itself up as %+v, want critical and nothing else", e)
	}

	counts := map[ses.Kind]int{}
	for i := range e.Elements {
		counts[e.Elements[i].Kind]++
	}
	want := map[ses.Kind]int{ses.KindArrayDeviceSlot: 24, ses.KindEnclosure: 1, ses.KindSASExpander: 1, ses.KindCooling: 5, ses.KindTemperatureSensor: 2, ses.KindVoltageSensor: 2, ses.KindSASConnector: 3, ses.KindPowerSupply: 2, ses.KindAudibleAlarm: 1}
	if len(e.Elements) != 41 || len(counts) != len(want) {
		t.Fatalf("read %d things of %d kinds, want 41 of %d: %v", len(e.Elements), len(counts), len(want), counts)
	}
	for kind, n := range want {
		if counts[kind] != n {
			t.Errorf("%d of kind %s, want %d", counts[kind], kind, n)
		}
	}

	for _, check := range []struct {
		kind   ses.Kind
		number int
		name   string
		status string
	}{
		{ses.KindArrayDeviceSlot, 0, "SLOT 01", "not_installed"},
		{ses.KindArrayDeviceSlot, 17, "SLOT 18", "not_installed"},
		{ses.KindArrayDeviceSlot, 18, "SLOT 19", "ok"},
		{ses.KindArrayDeviceSlot, 23, "SLOT 24", "not_installed"},
		{ses.KindEnclosure, 0, "EnclosureElement01", "ok"},
		{ses.KindSASExpander, 0, "Expander0", "ok"},
		{ses.KindCooling, 0, "Fan 01", "not_installed"},
		{ses.KindCooling, 4, "CPUFan", "ok"},
		{ses.KindTemperatureSensor, 0, "ENC. Temp", "ok"},
		{ses.KindTemperatureSensor, 1, "Chip Temp", "ok"},
		{ses.KindVoltageSensor, 0, "0.95V", "ok"},
		{ses.KindVoltageSensor, 1, "1.8V", "ok"},
		{ses.KindSASConnector, 2, "Connector02", "ok"},
		{ses.KindPowerSupply, 0, "PowerSupply01", "not_installed"},
		{ses.KindAudibleAlarm, 0, "Audible-Alarm", "ok"},
	} {
		if got := element(t, &e, check.kind, check.number); got.Name != check.name || got.StatusWord() != check.status {
			t.Errorf("%s %d is called %q and is %s, want %q and %s", check.kind, check.number, got.Name, got.StatusWord(), check.name, check.status)
		}
	}

	if rpm, ok := element(t, &e, ses.KindCooling, 4).RPM(); !ok || rpm != 7500 {
		t.Errorf("the one fan fitted turns at %v (%t), want 7500", rpm, ok)
	}
	if rpm, ok := element(t, &e, ses.KindCooling, 0).RPM(); ok {
		t.Errorf("a fan that is not fitted turns at %v", rpm)
	}
	if a, b := element(t, &e, ses.KindTemperatureSensor, 0), element(t, &e, ses.KindTemperatureSensor, 1); celsius(a) != 49 || celsius(b) != 66 {
		t.Errorf("the enclosure is at %v and its chip at %v, want 49 and 66", celsius(a), celsius(b))
	}
	if a, b := element(t, &e, ses.KindVoltageSensor, 0), element(t, &e, ses.KindVoltageSensor, 1); volts(a) != 0.94 || volts(b) != 1.8 {
		t.Errorf("the two rails are at %v and %v, want 0.94 and 1.8", volts(a), volts(b))
	}
	if element(t, &e, ses.KindPowerSupply, 0).Problems() != nil {
		t.Error("a power supply that is not fitted has troubles")
	}

	for i := range e.Elements {
		if e.Elements[i].Fault() {
			t.Errorf("%s %d is said to have failed, and nothing in this enclosure has", e.Elements[i].Kind, e.Elements[i].Number)
		}
	}

	// the limits it holds its two temperature sensors to, from a page that ends before its last things
	if err := e.Limit(pages[ses.PageConfiguration], pages[ses.PageLimits]); err != nil {
		t.Fatal(err)
	}
	for number, want := range map[int][4]float64{0: {79, 60, 5, 0}, 1: {95, 90, 5, 0}} {
		if held, set, some := limits(element(t, &e, ses.KindTemperatureSensor, number)); !some || set != [4]bool{true, true, true, true} || held != want {
			t.Errorf("temperature sensor %d is held to %v (set %v, some %t), want %v", number, held, set, some, want)
		}
	}
	if _, _, some := limits(element(t, &e, ses.KindCooling, 4)); some {
		t.Error("a fan has limits in degrees")
	}
}

// celsius is a sensor's temperature, or a number no enclosure gives when it gave none.
func celsius(e *ses.Element) float64 {
	if c, ok := e.Celsius(); ok {
		return c
	}

	return -1000
}

// volts is a sensor's voltage, or a number no enclosure gives when it gave none.
func volts(e *ses.Element) float64 {
	if v, ok := e.Volts(); ok {
		return v
	}

	return -1000
}
