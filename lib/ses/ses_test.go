package ses_test

import (
	"strings"
	"testing"

	"github.com/katbyte/sas-exporter/lib/ses"
	"github.com/katbyte/sas-exporter/lib/ses/sestest"
)

// backplane is an enclosure like a Supermicro backplane: two slots, two temperature sensors, two voltage sensors, three fans of which one is not
// fitted, two power supplies, and the connector and expander it reports on.
func backplane() *sestest.Shelf {
	return &sestest.Shelf{Generation: 7, Groups: []sestest.Group{
		{Kind: ses.KindArrayDeviceSlot, Things: []sestest.Thing{{Name: "Slot00", Status: sestest.OK}, {Name: "Slot01", Status: sestest.NotInstalled}}},
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Name: "Temperature1  ", Status: sestest.Temperature(34)}, {Name: "Temperature2", Status: sestest.Temperature(-5)}}},
		{Kind: ses.KindVoltageSensor, Things: []sestest.Thing{{Name: "5V", Status: sestest.Hundredths(504)}, {Name: "12V", Status: sestest.Hundredths(1204)}}},
		{Kind: ses.KindCooling, Things: []sestest.Thing{{Name: "Fan1", Status: sestest.Fan(5400)}, {Name: "Fan2", Status: [4]byte{0x02, 0, 0, 0x40}}, {Name: "Fan3", Status: sestest.NotInstalled}}},
		{Kind: ses.KindPowerSupply, Things: []sestest.Thing{{Name: "PS1", Status: sestest.OK}, {Name: "PS2", Status: [4]byte{0x02, 0, 0x04, 0x42}}}},
		{Kind: ses.KindSASConnector, Things: []sestest.Thing{{Status: [4]byte{0x01, 0x12, 0xff, 0x80}}}},
		{Kind: ses.KindSASExpander, Things: []sestest.Thing{{Name: "SAS3X40", Status: sestest.OK}}},
	}}
}

// parsed is the enclosure its three pages describe.
func parsed(t *testing.T, s *sestest.Shelf) ses.Enclosure {
	t.Helper()

	e, err := ses.Parse(s.Pages())
	if err != nil {
		t.Fatal(err)
	}

	return e
}

// element is the thing of that kind and number, and fails the test when the enclosure has none.
func element(t *testing.T, e *ses.Enclosure, kind ses.Kind, number int) *ses.Element {
	t.Helper()

	for i := range e.Elements {
		if e.Elements[i].Kind == kind && e.Elements[i].Number == number {
			return &e.Elements[i]
		}
	}
	t.Fatalf("no %s numbered %d among the %d things read", kind, number, len(e.Elements))

	return nil
}

// The three pages only make sense together: the first says what kinds of thing there are and how many, and the other two give a status and a name
// for each in that order. Everything is numbered from 0 within its kind, and named as the enclosure names it, less the padding.
func TestEveryThingHasItsKindNumberAndName(t *testing.T) {
	t.Parallel()

	e := parsed(t, backplane())
	if len(e.Elements) != 13 {
		t.Fatalf("read %d things, want 13", len(e.Elements))
	}

	for _, want := range []struct {
		kind   ses.Kind
		number int
		name   string
		status string
	}{
		{ses.KindArrayDeviceSlot, 0, "Slot00", "ok"},
		{ses.KindArrayDeviceSlot, 1, "Slot01", "not_installed"},
		{ses.KindTemperatureSensor, 0, "Temperature1", "ok"},
		{ses.KindCooling, 1, "Fan2", "critical"},
		{ses.KindCooling, 2, "Fan3", "not_installed"},
		{ses.KindPowerSupply, 1, "PS2", "critical"},
		{ses.KindSASConnector, 0, "", "ok"},
		{ses.KindSASExpander, 0, "SAS3X40", "ok"},
	} {
		if got := element(t, &e, want.kind, want.number); got.Name != want.name || got.StatusWord() != want.status {
			t.Errorf("%s %d is called %q and is %s, want %q and %s", want.kind, want.number, got.Name, got.StatusWord(), want.name, want.status)
		}
	}
}

// What each kind of sensor measures, in the units they are always given in: a fan in turns a minute, a temperature in degrees, which can be below
// freezing, a voltage in volts.
func TestWhatASensorMeasures(t *testing.T) {
	t.Parallel()

	e := parsed(t, backplane())

	if rpm, ok := element(t, &e, ses.KindCooling, 0).RPM(); !ok || rpm != 5400 {
		t.Errorf("the fan turns at %v (%t), want 5400", rpm, ok)
	}
	if c, ok := element(t, &e, ses.KindTemperatureSensor, 0).Celsius(); !ok || c != 34 {
		t.Errorf("the first sensor is at %v (%t), want 34", c, ok)
	}
	if c, ok := element(t, &e, ses.KindTemperatureSensor, 1).Celsius(); !ok || c != -5 {
		t.Errorf("the second sensor is at %v (%t), want -5", c, ok)
	}
	if v, ok := element(t, &e, ses.KindVoltageSensor, 1).Volts(); !ok || v != 12.04 {
		t.Errorf("the 12V rail is at %v (%t), want 12.04", v, ok)
	}

	// a fan is asked for its speed and nothing else is: a slot has no speed, and a fan no temperature
	if _, ok := element(t, &e, ses.KindArrayDeviceSlot, 0).RPM(); ok {
		t.Error("a slot gave a fan's speed")
	}
	if _, ok := element(t, &e, ses.KindCooling, 0).Celsius(); ok {
		t.Error("a fan gave a temperature")
	}
	if _, ok := element(t, &e, ses.KindVoltageSensor, 0).Amperes(); ok {
		t.Error("a voltage sensor gave a current")
	}
}

// A thing that is not fitted measures nothing, and a speed or a temperature of 0 for it would be a lie: it has none.
func TestAThingThatIsNotThereMeasuresNothing(t *testing.T) {
	t.Parallel()

	e := parsed(t, backplane())
	if rpm, ok := element(t, &e, ses.KindCooling, 2).RPM(); ok {
		t.Errorf("a fan that is not fitted turns at %v", rpm)
	}
	if problems := element(t, &e, ses.KindPowerSupply, 0).Problems(); len(problems) != 8 {
		t.Errorf("a power supply that is there has %d particular troubles it can be in, want 8", len(problems))
	}

	gone := sestest.Shelf{Groups: []sestest.Group{
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Status: sestest.NotInstalled}, {Status: sestest.OK}}},
		{Kind: ses.KindPowerSupply, Things: []sestest.Thing{{Status: sestest.NotInstalled}}},
		{Kind: ses.KindCurrentSensor, Things: []sestest.Thing{{Status: sestest.Hundredths(-250)}}},
	}}
	e = parsed(t, &gone)

	if c, ok := element(t, &e, ses.KindTemperatureSensor, 0).Celsius(); ok {
		t.Errorf("a sensor that is not fitted is at %v", c)
	}
	// a sensor that is there and gives a byte of 0 has given no temperature: the enclosure counts from 20 below
	if c, ok := element(t, &e, ses.KindTemperatureSensor, 1).Celsius(); ok {
		t.Errorf("a sensor that gave no temperature is at %v", c)
	}
	if problems := element(t, &e, ses.KindPowerSupply, 0).Problems(); problems != nil {
		t.Errorf("a power supply that is not fitted has troubles: %v", problems)
	}
	if a, ok := element(t, &e, ses.KindCurrentSensor, 0).Amperes(); !ok || a != -2.5 {
		t.Errorf("the current is %v (%t), want -2.5: it can flow either way", a, ok)
	}
}

// Whether a thing has failed is kept in a different place for each kind, and a power supply says which of several ways it is in trouble.
func TestWhatHasFailed(t *testing.T) {
	t.Parallel()

	e := parsed(t, backplane())
	for _, want := range []struct {
		kind   ses.Kind
		number int
		fault  bool
	}{{ses.KindCooling, 0, false}, {ses.KindCooling, 1, true}, {ses.KindPowerSupply, 0, false}, {ses.KindPowerSupply, 1, true}, {ses.KindSASConnector, 0, false}, {ses.KindArrayDeviceSlot, 0, false}} {
		if got := element(t, &e, want.kind, want.number).Fault(); got != want.fault {
			t.Errorf("%s %d: failed is %t, want %t", want.kind, want.number, got, want.fault)
		}
	}

	set := map[string]bool{}
	for _, problem := range element(t, &e, ses.KindPowerSupply, 1).Problems() {
		set[problem.Name] = problem.Set
	}
	if !set["ac_fail"] || !set["dc_undervoltage"] || set["dc_fail"] || set["off"] || set["overtemperature_fail"] {
		t.Errorf("the failed power supply's troubles = %v, want its mains lost and its output low, and nothing else", set)
	}

	sensors := sestest.Shelf{Groups: []sestest.Group{
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Status: [4]byte{0x02, 0x40, 80, 0x08}}}},
		{Kind: ses.KindEnclosure, Things: []sestest.Thing{{Status: [4]byte{0x01, 0, 0x02, 0}}, {Status: sestest.OK}}},
		{Kind: ses.Kind(0x7f), Things: []sestest.Thing{{Status: [4]byte{0x0f, 0xff, 0xff, 0xff}}}},
	}}
	e = parsed(t, &sensors)

	if !element(t, &e, ses.KindTemperatureSensor, 0).Fault() || !element(t, &e, ses.KindEnclosure, 0).Fault() || element(t, &e, ses.KindEnclosure, 1).Fault() {
		t.Error("a failed sensor and an enclosure showing a failure should have failed, and an enclosure showing none should not")
	}
	// a kind this program has never heard of is still listed, under its number, with whatever status it gives
	if odd := element(t, &e, ses.Kind(0x7f), 0); odd.Kind.String() != "kind_127" || odd.StatusWord() != "reserved" || odd.Fault() {
		t.Errorf("a kind nobody has heard of is a %s that is %s, failed %t; want kind_127, reserved and not failed", odd.Kind, odd.StatusWord(), odd.Fault())
	}
}

// An enclosure sums itself up in one byte, which is the quickest thing to raise an alarm on.
func TestHowAnEnclosureSumsItselfUp(t *testing.T) {
	t.Parallel()

	for conditions, want := range map[byte]ses.Enclosure{0x00: {}, 0x02: {Critical: true}, 0x04: {NonCritical: true}, 0x01: {Unrecoverable: true}, 0x08: {Info: true}, 0x06: {Critical: true, NonCritical: true}} {
		e := parsed(t, &sestest.Shelf{Conditions: conditions})
		if e.Info != want.Info || e.NonCritical != want.NonCritical || e.Critical != want.Critical || e.Unrecoverable != want.Unrecoverable {
			t.Errorf("conditions %#02x read as %+v, want %+v", conditions, e, want)
		}
	}
}

// The pages are asked for one after another, and an enclosure can change in between: a caddy pulled. Each page carries a number that changes when
// it does, and pages that disagree are refused rather than matched up wrongly.
func TestPagesThatDoNotBelongTogether(t *testing.T) {
	t.Parallel()

	before, after := backplane(), backplane()
	after.Generation = 8

	configuration, status, names := before.Pages()
	_, later, _ := after.Pages()

	if _, err := ses.Parse(configuration, later, names); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("pages of two generations = %v, want them refused as changed", err)
	}

	// a page that is not the one asked for, or is cut off, is refused the same way
	for name, pages := range map[string][3][]byte{
		"the status in place of the configuration":   {status, status, names},
		"the names in place of the status":           {configuration, names, names},
		"the status in place of the names":           {configuration, status, status},
		"no configuration":                           {nil, status, names},
		"no status":                                  {configuration, nil, names},
		"no names":                                   {configuration, status, nil},
		"a configuration cut off in its description": {configuration[:10], status, names},
		"a configuration cut off in its kinds":       {configuration[:50], status, names},
		"a status cut off":                           {configuration, status[:30], names},
	} {
		if e, err := ses.Parse(pages[0], pages[1], pages[2]); err == nil {
			t.Errorf("%s: read as %d things, want it refused", name, len(e.Elements))
		}
	}

	// names cut off part way leave the rest unnamed, and what the status says of them stands
	e, err := ses.Parse(configuration, status, names[:30])
	if err != nil || len(e.Elements) != 13 {
		t.Fatalf("with the names cut off: %v and %d things, want all 13 still read", err, len(e.Elements))
	}
	if first, last := element(t, &e, ses.KindArrayDeviceSlot, 0), element(t, &e, ses.KindSASExpander, 0); first.Name != "Slot00" || last.Name != "" || last.StatusWord() != "ok" {
		t.Errorf("with the names cut off the first is %q and the last %q (%s), want Slot00 and nothing, still ok", first.Name, last.Name, last.StatusWord())
	}
}

// limits is the four limits a thing is held to, with nothing for one that is not set and false for a thing with none at all.
func limits(e *ses.Element) (held [4]float64, set [4]bool, some bool) {
	l, some := e.Limits()
	for i, limit := range []*float64{l.HighCritical, l.HighWarning, l.LowWarning, l.LowCritical} {
		if limit != nil {
			held[i], set[i] = *limit, true
		}
	}

	return held, set, some
}

// An enclosure holds each temperature sensor to four limits, given as temperatures are and in a page laid out as the statuses are. A limit it does
// not set is left out rather than read as a temperature, and a sensor with none set has no limits at all. Voltage sensors have limits too, but as
// a share of what they should read, which the enclosure does not say: those are not given.
func TestTheLimitsASensorIsHeldTo(t *testing.T) {
	t.Parallel()

	s := &sestest.Shelf{Groups: []sestest.Group{
		{Kind: ses.KindArrayDeviceSlot, Things: []sestest.Thing{{Name: "Slot00", Status: sestest.OK}}},
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{
			{Name: "ChipDie", Status: sestest.Temperature(70), Limits: sestest.Limits(105, 100, 0, -4)},
			{Name: "Inlet", Status: sestest.Temperature(21), Limits: [4]byte{70, 66, 0, 0}},
			{Name: "Spare", Status: sestest.Temperature(30)},
		}},
		{Kind: ses.KindVoltageSensor, Things: []sestest.Thing{{Name: "12V", Status: sestest.Hundredths(1204), Limits: [4]byte{30, 20, 20, 30}}}},
	}}
	e := parsed(t, s)

	// before the page is read nothing has limits
	if _, _, some := limits(element(t, &e, ses.KindTemperatureSensor, 0)); some {
		t.Error("a sensor has limits before the page that holds them was read")
	}

	configuration, _, _ := s.Pages()
	if err := e.Limit(configuration, s.Limits()); err != nil {
		t.Fatal(err)
	}

	if held, set, some := limits(element(t, &e, ses.KindTemperatureSensor, 0)); !some || set != [4]bool{true, true, true, true} || held != [4]float64{105, 100, 0, -4} {
		t.Errorf("the chip is held to %v (set %v, some %t), want 105, 100, 0 and -4", held, set, some)
	}
	if held, set, some := limits(element(t, &e, ses.KindTemperatureSensor, 1)); !some || set != [4]bool{true, true, false, false} || held != [4]float64{50, 46, 0, 0} {
		t.Errorf("the inlet is held to %v (set %v, some %t), want 50 and 46 and no low limits", held, set, some)
	}
	if _, _, some := limits(element(t, &e, ses.KindTemperatureSensor, 2)); some {
		t.Error("a sensor the enclosure sets no limits for has some")
	}
	if _, _, some := limits(element(t, &e, ses.KindVoltageSensor, 0)); some {
		t.Error("a voltage sensor has limits in degrees")
	}
	if _, _, some := limits(element(t, &e, ses.KindArrayDeviceSlot, 0)); some {
		t.Error("a slot has limits")
	}
}

// A real enclosure may end its page of limits before the last of its things, as the Areca one does: what the page covers has its limits and the
// rest has none. A page that is another page, or of the enclosure as it was before something in it changed, is refused.
func TestAPageOfLimitsThatDoesNotFit(t *testing.T) {
	t.Parallel()

	s := &sestest.Shelf{Generation: 7, Groups: []sestest.Group{
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Name: "First", Status: sestest.Temperature(30), Limits: sestest.Limits(50, 46, 10, 0)}, {Name: "Second", Status: sestest.Temperature(30), Limits: sestest.Limits(60, 56, 10, 0)}}},
	}}
	configuration, _, _ := s.Pages()
	whole := s.Limits()

	e := parsed(t, s)
	if err := e.Limit(configuration, whole[:len(whole)-4]); err != nil {
		t.Fatalf("a page that ends early: %v", err)
	}
	if _, _, first := limits(element(t, &e, ses.KindTemperatureSensor, 0)); !first {
		t.Error("the sensor the short page covers has no limits")
	}
	if _, _, second := limits(element(t, &e, ses.KindTemperatureSensor, 1)); second {
		t.Error("the sensor the short page ends before has limits")
	}

	_, status, _ := s.Pages()
	later := &sestest.Shelf{Generation: 8, Groups: s.Groups}
	for name, page := range map[string][]byte{"the status page": status, "a page of the enclosure once it had changed": later.Limits(), "four bytes": whole[:4]} {
		e := parsed(t, s)
		if err := e.Limit(configuration, page); err == nil {
			t.Errorf("%s was taken for the page of limits", name)
		}
		if _, _, some := limits(element(t, &e, ses.KindTemperatureSensor, 0)); some {
			t.Errorf("%s gave a sensor limits", name)
		}
	}
}
