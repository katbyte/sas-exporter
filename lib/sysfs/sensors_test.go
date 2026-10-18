package sysfs_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/katbyte/go-kt/pointer"
	"github.com/katbyte/sas-exporter/lib/mpt"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/ses"
	"github.com/katbyte/sas-exporter/lib/ses/sestest"
	"github.com/katbyte/sas-exporter/lib/sysfs"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
)

// hardware stands in for the devices the hardware itself is asked through: enclosures that answer with the pages they were given, by the name of
// the device each is asked through, and cards that answer with theirs, by the number their driver knows them by. It counts what it was asked.
type hardware struct {
	shelves map[string]*sestest.Shelf
	// cards is each card's page with its temperatures in, and pages every other page of its own it answers with, by what was asked
	cards map[uint32][]byte
	pages map[uint32]map[mpt.Page][]byte
	// failing is what every question of that kind fails with, when it is set
	failEnclosures error
	failCards      error
	// noLimits makes every enclosure refuse the page of limits it holds its sensors to, and noLanes the page that says which connector each lane
	// is wired to, as one that has no such page does
	noLimits bool
	noLanes  bool
	asked    []string
	// limits and lanes count the times each of those pages was asked for, and cardAsks each of a card's own pages
	limits   int
	lanes    int
	cardAsks map[mpt.Page]int
}

func (h *hardware) devices() sysfs.Devices {
	return sysfs.Devices{
		Diagnostic: func(_ context.Context, device string, page byte) ([]byte, error) {
			h.asked = append(h.asked, device)
			if h.failEnclosures != nil {
				return nil, h.failEnclosures
			}

			shelf, known := h.shelves[device]
			if !known {
				return nil, fs.ErrNotExist
			}

			if page == ses.PageLimits {
				h.limits++
				if h.noLimits {
					return nil, errors.New("illegal request: the enclosure has no such page")
				}
			}
			if page == ses.PageAdditional {
				h.lanes++
				if h.noLanes {
					return nil, errors.New("illegal request: the enclosure has no such page")
				}
			}

			configuration, status, names := shelf.Pages()

			return map[byte][]byte{ses.PageConfiguration: configuration, ses.PageStatus: status, ses.PageNames: names, ses.PageLimits: shelf.Limits(), ses.PageAdditional: shelf.Additional()}[page], nil
		},
		CardPage: func(_ context.Context, driver string, id uint32, page mpt.Page) ([]byte, error) {
			h.asked = append(h.asked, driver)
			if h.failCards != nil {
				return nil, h.failCards
			}
			if h.cardAsks == nil {
				h.cardAsks = map[mpt.Page]int{}
			}
			h.cardAsks[page]++
			if page == mpt.IOUnit7 {
				return h.cards[id], nil
			}

			return h.pages[id][page], nil
		},
	}
}

// temperatures is a card's page with its processor at one temperature and its board at another, both in Celsius.
func temperatures(chip, board byte) []byte {
	page := make([]byte, 0x28)
	page[0x10], page[0x12] = chip, 2
	page[0x14], page[0x16] = board, 2

	return page
}

// machine is a card with a backplane behind it holding a disk in a slot, and what the backplane's enclosure says of everything else in it: two
// fans of which one has failed and was expected to, a temperature, a voltage, two power supplies of which one is being pointed out and one has
// lost its mains, the expander itself, and two connectors with no name, the backplane's first two phys wired to the first and its third to the
// second.
func machine(t *testing.T) (*sysfstest.Tree, *sysfstest.Shelf, *hardware) {
	t.Helper()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 3, sysfstest.Rate12)
	backplane := node.Expander(0, 1, 2, 3)
	backplane.Phys(0, 1, sysfstest.Rate12)
	shelf := backplane.Enclosure(1)
	shelf.Slot(0, "OK", backplane.Disk(0, "sda", "Z0000001"))

	// a card that has slowed to half speed on reduced power
	card := temperatures(53, 41)
	card[0x04], card[0x13] = 0x05, 0x02

	h := &hardware{
		cards: map[uint32][]byte{0: card},
		shelves: map[string]*sestest.Shelf{shelf.Device: {Conditions: 0x02, Groups: []sestest.Group{
			{Kind: ses.KindArrayDeviceSlot, Things: []sestest.Thing{{Name: "Slot00", Status: sestest.OK}}},
			{Kind: ses.KindCooling, Things: []sestest.Thing{{Name: "Fan1", Status: sestest.Fan(5400)}, {Name: "Fan2", Status: [4]byte{0x42, 0, 0, 0x40}}}},
			{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Name: "Backplane", Status: sestest.Temperature(34), Limits: sestest.Limits(50, 46, 10, 0)}}},
			{Kind: ses.KindVoltageSensor, Things: []sestest.Thing{{Name: "12V", Status: sestest.Hundredths(1204)}}},
			{Kind: ses.KindPowerSupply, Things: []sestest.Thing{{Name: "PS1", Status: [4]byte{0x01, 0x80, 0, 0}}, {Name: "PS2", Status: [4]byte{0x02, 0, 0, 0x42}}}},
			{Kind: ses.KindSASExpander, Things: []sestest.Thing{{Name: "Expander", Status: sestest.OK}}},
			{Kind: ses.KindSASConnector, Things: []sestest.Thing{{Status: sestest.OK}, {Status: sestest.OK}}},
		}, Wiring: []sestest.Wiring{{Address: backplane.Address(), Connectors: []byte{0, 0, 1, 0xff}}}}},
	}

	return tree, shelf, h
}

// The pages a made-up card answers with, laid out as a real one lays them out.

// manufacturing7 is a card's page naming the connector each of its phys is wired to, one name for each phy from the first.
func manufacturing7(names ...string) []byte {
	page := make([]byte, 0x24+len(names)*0x1c)
	page[0x20] = byte(len(names)) //nolint:gosec // G115: a test's own few phys
	for phy, name := range names {
		copy(page[0x24+phy*0x1c+4:], name)
		page[0x24+phy*0x1c+0x14] = 0x02
	}

	return page
}

// phyPage is a card's page about one of its own phys, whose link has changed that many times.
func phyPage(changes byte) []byte {
	page := make([]byte, 0x24)
	page[0x16] = changes

	return page
}

// expanderPage is a card's page about an expander it has found, by its address, its handle, how many phys it has and how often it has changed.
func expanderPage(address [8]byte, handle uint16, phys byte, changes uint16) []byte {
	page := make([]byte, 0x30)
	binary.LittleEndian.PutUint64(page[0x0c:], binary.BigEndian.Uint64(address[:]))
	binary.LittleEndian.PutUint16(page[0x18:], handle)
	binary.LittleEndian.PutUint16(page[0x1c:], changes)
	page[0x20] = phys

	return page
}

// expanderPhyPage is a card's page about one phy of an expander, whose link has changed that many times.
func expanderPhyPage(phy, changes byte) []byte {
	page := make([]byte, 0x2c)
	page[0x1e], page[0x20] = changes, phy

	return page
}

// part is the thing of that kind and name in what an enclosure said, and fails the test when it is not there.
func part(t *testing.T, e *poll.EnclosureSensors, kind, name string) *poll.Element {
	t.Helper()

	for i := range e.Elements {
		if e.Elements[i].Kind == kind && e.Elements[i].Name == name {
			return &e.Elements[i]
		}
	}
	t.Fatalf("no %s called %q among the %d things the enclosure reports", kind, name, len(e.Elements))

	return nil
}

// What the hardware says of itself when asked: the card its own temperatures, and each enclosure everything in it that is not a slot, each thing
// under the name the enclosure gives it, with what it measures in the units it is always given in.
func TestWhatTheHardwareSaysOfItself(t *testing.T) {
	t.Parallel()

	tree, shelf, h := machine(t)
	got, err := sysfs.New(tree.Root, h.devices()).Sensors(t.Context(), card)
	if err != nil || got.Unread != nil || got.Unavailable != "" {
		t.Fatalf("sensors: %v, with %v unread and %q unavailable; want none of those", err, got.Unread, got.Unavailable)
	}

	if len(got.Temperatures) != 2 || got.Temperatures[0] != (poll.Temperature{Sensor: "chip", Celsius: 53}) || got.Temperatures[1] != (poll.Temperature{Sensor: "board", Celsius: 41}) {
		t.Errorf("the card's temperatures = %+v, want its chip at 53 and its board at 41", got.Temperatures)
	}
	if got.Speed != 0.5 || got.PowerMode != "reduced" {
		t.Errorf("the card runs at %v of its speed on %q power, want half on reduced", got.Speed, got.PowerMode)
	}

	if len(got.Enclosures) != 1 || got.Enclosures[0].Name != shelf.Name {
		t.Fatalf("enclosures = %+v, want the one, under the name the kernel gives it", got.Enclosures)
	}
	e := &got.Enclosures[0]
	if !e.Conditions.Critical || e.Conditions.NonCritical || e.Conditions.Unrecoverable || e.Conditions.Info {
		t.Errorf("the enclosure sums itself up as %+v, want critical and nothing else", e.Conditions)
	}

	// the slot is left to what the kernel already says of it
	if len(e.Elements) != 9 {
		t.Fatalf("the enclosure reports %d things, want the 9 that are not slots", len(e.Elements))
	}

	if fan := part(t, e, "cooling", "Fan1"); fan.Status != "ok" || fan.Fault || fan.RPM == nil || pointer.From(fan.RPM) != 5400 || fan.Celsius != nil {
		t.Errorf("the first fan = %+v, want it ok at 5400 turns a minute", fan)
	}
	if fan := part(t, e, "cooling", "Fan2"); fan.Status != "critical" || !fan.Fault || !fan.PredictedFailure {
		t.Errorf("the second fan = %+v, want it critical and failed, as the enclosure expected", fan)
	}
	if fan := part(t, e, "cooling", "Fan1"); fan.PredictedFailure || fan.Locate == nil || pointer.From(fan.Locate) {
		t.Errorf("the first fan = %+v, want it expected to last and its locate light off", fan)
	}
	if supply := part(t, e, "power_supply", "PS1"); supply.Status != "ok" || supply.Locate == nil || !pointer.From(supply.Locate) {
		t.Errorf("the first power supply = %+v, want it ok with its locate light on", supply)
	}
	if sensor := part(t, e, "temperature_sensor", "Backplane"); sensor.Celsius == nil || pointer.From(sensor.Celsius) != 34 || sensor.RPM != nil {
		t.Errorf("the temperature sensor = %+v, want 34 degrees", sensor)
	}
	if sensor := part(t, e, "voltage_sensor", "12V"); sensor.Volts == nil || pointer.From(sensor.Volts) != 12.04 {
		t.Errorf("the voltage sensor = %+v, want 12.04 volts", sensor)
	}

	lost := map[string]bool{}
	for _, problem := range part(t, e, "power_supply", "PS2").Problems {
		lost[problem.Name] = problem.Set
	}
	if len(lost) != 8 || !lost["ac_fail"] || lost["dc_fail"] || !part(t, e, "power_supply", "PS2").Fault {
		t.Errorf("the second power supply's troubles = %v, want it failed with its mains lost and nothing else", lost)
	}

	// what the enclosure gives no name goes by its number among those of its kind
	part(t, e, "sas_connector", "0")
	part(t, e, "sas_connector", "1")

	// each lane under the kernel's name for its expander, wired to a connector under the name the enclosure gives it; one wired to none is left out
	want := []poll.Lane{{Expander: "expander-1:0", Phy: 0, Connector: "0"}, {Expander: "expander-1:0", Phy: 1, Connector: "0"}, {Expander: "expander-1:0", Phy: 2, Connector: "1"}}
	if !slices.Equal(e.Lanes, want) {
		t.Errorf("lanes = %+v, want %+v", e.Lanes, want)
	}
}

// An enclosure that has no page saying which connector each lane is wired to is reported without, and is not asked for it again.
func TestAnEnclosureThatDoesNotSayWhereItsLanesGo(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	h.noLanes = true
	k := sysfs.New(tree.Root, h.devices())
	for range 3 {
		got, err := k.Sensors(t.Context(), card)
		if err != nil || got.Unread != nil || got.Unavailable != "" || len(got.Enclosures) != 1 || len(got.Enclosures[0].Lanes) != 0 || len(got.Enclosures[0].Elements) != 9 {
			t.Fatalf("with no page of lanes: %v, %v unread, %q unavailable, %d enclosures; want the one, without lanes and nothing amiss", err, got.Unread, got.Unavailable, len(got.Enclosures))
		}
	}
	if h.lanes != 1 {
		t.Errorf("the enclosure was asked for a page it does not have %d times in three rounds, want once", h.lanes)
	}
}

// Two of a kind that an enclosure calls the same would be one on the page, so each after the first has its number added.
func TestTwoThingsAnEnclosureCallsTheSame(t *testing.T) {
	t.Parallel()

	tree, shelf, h := machine(t)
	h.shelves[shelf.Device] = &sestest.Shelf{Groups: []sestest.Group{
		{Kind: ses.KindCooling, Things: []sestest.Thing{{Name: "Fan", Status: sestest.Fan(5000)}, {Name: "Fan", Status: sestest.Fan(5100)}, {Name: "Fan", Status: sestest.Fan(5200)}}},
		{Kind: ses.KindTemperatureSensor, Things: []sestest.Thing{{Name: "Fan", Status: sestest.Temperature(30)}}},
	}}

	got, err := sysfs.New(tree.Root, h.devices()).Sensors(t.Context(), card)
	if err != nil || len(got.Enclosures) != 1 {
		t.Fatalf("sensors: %v and %d enclosures, want the one", err, len(got.Enclosures))
	}

	e := &got.Enclosures[0]
	for name, rpm := range map[string]float64{"Fan": 5000, "Fan 1": 5100, "Fan 2": 5200} {
		if fan := part(t, e, "cooling", name); pointer.From(fan.RPM) != rpm {
			t.Errorf("the fan called %q turns at %v, want %v", name, pointer.From(fan.RPM), rpm)
		}
	}
	// a thing of another kind may share the name: its kind tells them apart
	part(t, e, "temperature_sensor", "Fan")
}

// A program with no leave to open the devices is the ordinary case, not a fault: nothing is read, what was kept from it is said in words for the
// log, and nothing is counted as unread. The first enclosure refused is the last one tried, since the rest would be refused the same way.
func TestWithNoLeaveToAskTheHardware(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	tree.Card(8)
	h.failEnclosures, h.failCards = fs.ErrPermission, fs.ErrPermission

	got, err := sysfs.New(tree.Root, h.devices()).Sensors(t.Context(), card)
	if err != nil || got.Unread != nil || !got.Nothing() {
		t.Fatalf("sensors: %v, with %v unread and %+v read; want nothing read and nothing wrong", err, got.Unread, got)
	}
	if !strings.Contains(got.Unavailable, "the card's temperature") || !strings.Contains(got.Unavailable, "the enclosures' sensors") || !strings.Contains(got.Unavailable, "permission denied") {
		t.Errorf("unavailable = %q, want both kinds named with why", got.Unavailable)
	}

	// and with neither asked for, nothing is opened and nothing is said to be missing
	h.asked = nil
	got, err = sysfs.New(tree.Root, sysfs.Devices{}).Sensors(t.Context(), card)
	if err != nil || !got.Nothing() || got.Unavailable != "" || len(h.asked) != 0 {
		t.Errorf("with nothing asked for: %v, %+v, and %d questions put; want nothing at all", err, got, len(h.asked))
	}
}

// Hardware that is asked and will not answer is a failure, said as one, and what did answer is still reported: an enclosure that is busy must
// not cost the card its temperature.
func TestHardwareThatWillNotAnswer(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	h.failEnclosures = errors.New("status 0x02, sense 05 24 00")

	got, err := sysfs.New(tree.Root, h.devices()).Sensors(t.Context(), card)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unread == nil || !strings.Contains(got.Unread.Error(), "the enclosures' sensors") || got.Unavailable != "" {
		t.Errorf("unread = %v and unavailable = %q, want the enclosure said to be unread and nothing unavailable", got.Unread, got.Unavailable)
	}
	if len(got.Temperatures) != 2 || len(got.Enclosures) != 0 {
		t.Errorf("read %d temperatures and %d enclosures, want the card's two and no enclosure", len(got.Temperatures), len(got.Enclosures))
	}
}

// A card that fails to give its temperature is asked the once. Its driver resets a card that does not answer, and a program that asked again every
// minute would reset it every minute: after the first failure it is left alone, and that is said rather than counted as failing again.
func TestACardThatFailsIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	h.failCards = errors.New("no data available")
	k := sysfs.New(tree.Root, h.devices())

	first, err := k.Sensors(t.Context(), card)
	if err != nil || first.Unread == nil || !strings.Contains(first.Unread.Error(), "the card's temperature") {
		t.Fatalf("the first time: %v, with %v unread; want the card's temperature unread", err, first.Unread)
	}

	h.failCards, h.asked = nil, nil
	second, err := k.Sensors(t.Context(), card)
	if err != nil || second.Unread != nil || len(second.Temperatures) != 0 || !strings.Contains(second.Unavailable, "not asked again") {
		t.Errorf("the second time: %v, %v unread, %d temperatures, unavailable %q; want it left alone and that said", err, second.Unread, len(second.Temperatures), second.Unavailable)
	}
	for _, question := range h.asked {
		if question == "mpt3sas" {
			t.Error("the card was asked again after it had failed")
		}
	}
	// the enclosures are another matter and carry on
	if len(second.Enclosures) != 1 {
		t.Errorf("read %d enclosures the second time, want the one still", len(second.Enclosures))
	}

	// being kept out is not the card failing: a program given leave later is not held to an earlier refusal
	tree, _, h = machine(t)
	h.failCards = fs.ErrPermission
	k = sysfs.New(tree.Root, h.devices())
	if _, err := k.Sensors(t.Context(), card); err != nil {
		t.Fatal(err)
	}

	h.failCards = nil
	if got, err := k.Sensors(t.Context(), card); err != nil || len(got.Temperatures) != 2 {
		t.Errorf("after leave was given: %v and %d temperatures, want the card's two", err, len(got.Temperatures))
	}

	// nor is a driver that is busy, with the card being reset say: the question never reached the card, so it is unread this time and asked the next
	tree, _, h = machine(t)
	h.failCards = fmt.Errorf("asking card 0: %w", syscall.EAGAIN)
	k = sysfs.New(tree.Root, h.devices())
	if busy, err := k.Sensors(t.Context(), card); err != nil || busy.Unread == nil {
		t.Fatalf("a busy driver: %v, with %v unread; want the card's temperature unread", err, busy.Unread)
	}

	h.failCards = nil
	if got, err := k.Sensors(t.Context(), card); err != nil || len(got.Temperatures) != 2 {
		t.Errorf("once the driver was free: %v and %d temperatures, want the card's two", err, len(got.Temperatures))
	}
}

// A card is asked through its driver by the number the driver knows it by, so a card whose driver gives it none cannot be asked and simply has no
// temperature, and a card with no such page has none either.
func TestACardWithNoTemperatureToGive(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	second := tree.Card(8)
	tree.Remove(second.Attrs(), "unique_id")
	k := sysfs.New(tree.Root, h.devices())

	if got, err := k.Sensors(t.Context(), "host8"); err != nil || len(got.Temperatures) != 0 || got.Unread != nil || got.Unavailable != "" {
		t.Errorf("a card with no number: %v, %+v; want no temperature and nothing said to be wrong", err, got)
	}

	// the first card is number 0 to its driver, and here has no page to give
	h.cards = nil
	if got, err := k.Sensors(t.Context(), card); err != nil || len(got.Temperatures) != 0 || got.Unread != nil || len(got.Enclosures) != 1 {
		t.Errorf("a card with no such page: %v, %+v; want no temperature, nothing wrong, and its enclosure still read", err, got)
	}
}

// An enclosure whose pages do not belong together, because it changed while it was being asked, is unread this time and asked again the next.
func TestAnEnclosureThatChangesWhileItIsAsked(t *testing.T) {
	t.Parallel()

	tree, shelf, h := machine(t)
	devices := h.devices()
	inner := devices.Diagnostic
	devices.Diagnostic = func(ctx context.Context, device string, page byte) ([]byte, error) {
		if page == ses.PageStatus {
			h.shelves[shelf.Device].Generation++
		}

		return inner(ctx, device, page)
	}

	got, err := sysfs.New(tree.Root, devices).Sensors(t.Context(), card)
	if err != nil || got.Unread == nil || !strings.Contains(got.Unread.Error(), "changed") || !strings.Contains(got.Unread.Error(), shelf.Name) {
		t.Errorf("sensors: %v, with %v unread; want the enclosure named as having changed", err, got.Unread)
	}
}

// A capture made as root holds the pages each enclosure answered with, by the device it was asked through, so that a real machine's fans and
// temperatures can be read in a test as they are on the machine. What is read from pages that have been through a capture is what the enclosure
// said.
func TestTheHardwareAsACaptureSavedIt(t *testing.T) {
	t.Parallel()

	tree, shelf, h := machine(t)
	configuration, status, names := h.shelves[shelf.Device].Pages()

	var lines strings.Builder
	for number, page := range map[byte][]byte{ses.PageConfiguration: configuration, ses.PageStatus: status, ses.PageNames: names} {
		fmt.Fprintf(&lines, "s\t%s/%02x\t%x\n", shelf.Device, number, page)
	}
	capture, err := sysfstest.Lay(t.TempDir(), lines.String())
	if err != nil || len(capture.Pages[shelf.Device]) != 3 {
		t.Fatalf("the capture holds %d pages of %s (%v), want its three", len(capture.Pages[shelf.Device]), shelf.Device, err)
	}

	got, err := sysfs.New(tree.Root, sysfs.Devices{Diagnostic: capture.Diagnostic}).Sensors(t.Context(), card)
	if err != nil || got.Unread != nil || got.Unavailable != "" || len(got.Enclosures) != 1 {
		t.Fatalf("sensors from the saved pages: %v, %v unread, %q unavailable and %d enclosures; want the one enclosure and nothing amiss", err, got.Unread, got.Unavailable, len(got.Enclosures))
	}
	if fan := part(t, &got.Enclosures[0], "cooling", "Fan1"); fan.RPM == nil || *fan.RPM != 5400 {
		t.Errorf("the fan turns at %v, want the 5400 the enclosure said", fan.RPM)
	}

	for _, bad := range []string{"s\tsg26\t00", "s\tsg26/zz\t00", "s\tsg26/02\tnot hex"} {
		if _, err := sysfstest.Lay(t.TempDir(), bad); err == nil {
			t.Errorf("a capture with the line %q was taken, want it refused", bad)
		}
	}
}

// The real machine's enclosures, read from the pages they answered with. The two backplanes behind the first card know of nothing but their own
// two temperatures and two voltages: that machine's fans and power supplies are plugged into its motherboard. The two behind the second share a
// chassis, and each reports its seven fans, two power supplies, three temperatures and ten voltages.
//
// Three of the four also say that something in them is critical or not quite right while every part they list reads ok. That is reported as the
// enclosure said it, and is why the condition is a reading of its own and not worked out from the parts.
func TestTheRealMachinesEnclosures(t *testing.T) {
	t.Parallel()

	root, capture := sysfstest.NAS(t)
	k := sysfs.New(root, sysfs.Devices{Diagnostic: capture.Diagnostic})

	var enclosures, fans, supplies, temperatures, voltages int
	conditions := map[string]poll.Conditions{}
	for _, hba := range []string{"host1", "host8"} {
		got, err := k.Sensors(t.Context(), hba)
		if err != nil || got.Unread != nil || got.Unavailable != "" || len(got.Temperatures) != 0 {
			t.Fatalf("%s: %v, %v unread, %q unavailable and %d temperatures of the card, which was not asked; want its enclosures and nothing amiss", hba, err, got.Unread, got.Unavailable, len(got.Temperatures))
		}

		enclosures += len(got.Enclosures)
		for i := range got.Enclosures {
			e := &got.Enclosures[i]
			conditions[e.Name] = e.Conditions

			names := map[string]bool{}
			for j := range e.Elements {
				part := &e.Elements[j]
				if names[part.Kind+" "+part.Name] {
					t.Errorf("%s: two things of the kind %s are called %q", e.Name, part.Kind, part.Name)
				}
				names[part.Kind+" "+part.Name] = true

				// the one thing none of them will speak for is the enclosure itself
				if want := map[bool]string{true: "unsupported", false: "ok"}[part.Kind == "enclosure"]; part.Status != want || part.Fault {
					t.Errorf("%s: the %s %q is %s, failed %t; want %s and not failed", e.Name, part.Kind, part.Name, part.Status, part.Fault, want)
				}
				// nothing is expected to fail, and the JBOD's backplanes say the locate light of each power supply is on, which is what they send
				if lit := part.Locate != nil && *part.Locate; part.PredictedFailure || lit != (part.Kind == "power_supply") {
					t.Errorf("%s: the %s %q is expected to fail %t, with its locate light on %t; want only the power supplies' lights on", e.Name, part.Kind, part.Name, part.PredictedFailure, lit)
				}

				switch part.Kind {
				case "cooling":
					fans++
					// a fan's speed follows how warm the chassis is: these have been seen from 4900 to 5800
					if part.RPM == nil || *part.RPM < 4000 || *part.RPM > 7000 {
						t.Errorf("%s: the fan %s turns at %v, want between 4000 and 7000 turns a minute", e.Name, part.Name, pointer.From(part.RPM))
					}
				case "power_supply":
					supplies++
					if len(part.Problems) != 8 || slices.ContainsFunc(part.Problems, func(p poll.Problem) bool { return p.Set }) || !strings.HasPrefix(part.Name, "PS0") {
						t.Errorf("%s: the power supply %q has %+v; want the eight kinds of trouble, none of them set, and a made-up name", e.Name, part.Name, part.Problems)
					}
				case "temperature_sensor":
					temperatures++
					if part.Celsius == nil || *part.Celsius < 30 || *part.Celsius > 80 {
						t.Errorf("%s: the sensor %s reads %v, want between 30 and 80 degrees", e.Name, part.Name, pointer.From(part.Celsius))
					}
				case "voltage_sensor":
					voltages++
					if part.Volts == nil || *part.Volts < 1 || *part.Volts > 12.5 {
						t.Errorf("%s: the sensor %s reads %v, want between 1 and 12.5 volts", e.Name, part.Name, pointer.From(part.Volts))
					}
				}
			}
		}
	}

	if enclosures != sysfstest.NASEnclosures || fans != sysfstest.NASFans || supplies != sysfstest.NASSupplies || temperatures != sysfstest.NASTemperatures || voltages != sysfstest.NASVoltages {
		t.Errorf("read %d enclosures, %d fans, %d power supplies, %d temperatures and %d voltages; want %d, %d, %d, %d and %d", enclosures, fans, supplies, temperatures, voltages, sysfstest.NASEnclosures, sysfstest.NASFans, sysfstest.NASSupplies, sysfstest.NASTemperatures, sysfstest.NASVoltages)
	}

	want := map[string]poll.Conditions{"1:0:24:0": {}, "1:0:35:0": {NonCritical: true}, "8:0:24:0": {Critical: true}, "8:0:25:0": {Critical: true}}
	if !maps.Equal(conditions, want) {
		t.Errorf("the enclosures say of themselves %+v, want %+v", conditions, want)
	}

	// a few readings as that machine gave them: a fan, the chip of a backplane, and the twelve volts it is fed
	second, err := k.Sensors(t.Context(), "host8")
	if err != nil || len(second.Enclosures) != 2 {
		t.Fatalf("host8 again: %v and %d enclosures", err, len(second.Enclosures))
	}
	shelf := &second.Enclosures[0]
	if fan, chip, fed := pointer.From(part(t, shelf, "cooling", "FAN04").RPM), pointer.From(part(t, shelf, "temperature_sensor", "ChipDie").Celsius), pointer.From(part(t, shelf, "voltage_sensor", "12VEXP").Volts); shelf.Name != "8:0:24:0" || fan != 5100 || chip != 66 || fed != 12.06 {
		t.Errorf("%s: FAN04 at %v, ChipDie at %v and 12VEXP at %v; want 8:0:24:0 with 5100, 66 and 12.06", shelf.Name, fan, chip, fed)
	}

	// the limits the first card's backplane holds its sensors to: its chip and board in degrees, its twelve volts as a share of twelve
	first, err := k.Sensors(t.Context(), "host1")
	if err != nil || len(first.Enclosures) != 2 || first.Enclosures[0].Name != "1:0:24:0" {
		t.Fatalf("host1 again: %v and %+v", err, first.Enclosures)
	}
	shelf = &first.Enclosures[0]
	for name, want := range map[string][4]float64{"ChipDie": {105, 100, 0, -4}, "BackPlaneLM75": {50, 46, 10, 0}, "12VEXP": {0.15, 0.1, -0.1, -0.15}} {
		kind := map[bool]string{true: "voltage_sensor", false: "temperature_sensor"}[name == "12VEXP"]
		held := part(t, shelf, kind, name).Limits
		if held == nil || [4]float64{pointer.From(held.HighCritical), pointer.From(held.HighWarning), pointer.From(held.LowWarning), pointer.From(held.LowCritical)} != want {
			t.Errorf("%s: %s is held to %+v, want %v", shelf.Name, name, held, want)
		}
	}

	// which connector each lane is wired to: every slot's own, and four lanes to each cable connector, the card's cable on C and D of the first
	// backplane and the cable on to the second on A and B
	lanes, wired := 0, map[string]string{}
	for _, hba := range []string{"host1", "host8"} {
		got, _ := k.Sensors(t.Context(), hba)
		for i := range got.Enclosures {
			lanes += len(got.Enclosures[i].Lanes)
			for _, lane := range got.Enclosures[i].Lanes {
				wired[fmt.Sprintf("%s %d", lane.Expander, lane.Phy)] = lane.Connector
			}
		}
	}
	if lanes != sysfstest.NASLanes {
		t.Errorf("%d lanes are wired to a connector, want %d", lanes, sysfstest.NASLanes)
	}
	for lane, want := range map[string]string{"expander-1:0 12": "ConnectorD", "expander-1:0 19": "ConnectorC", "expander-1:1 12": "ConnectorA", "expander-8:0 24": "ConnectorA", "expander-8:1 35": "ConnectorD"} {
		if wired[lane] != want {
			t.Errorf("%s is wired to %q, want %s", lane, wired[lane], want)
		}
	}
}

// The limits an enclosure holds its temperature sensors to are read with the rest. Not every enclosure has that page, and one that refuses it loses
// only the limits: everything else is read, nothing is counted as unread, and it is not asked for the page again.
func TestTheLimitsAnEnclosureHoldsItsSensorsTo(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	got, err := sysfs.New(tree.Root, h.devices()).Sensors(t.Context(), card)
	if err != nil || got.Unread != nil || len(got.Enclosures) != 1 {
		t.Fatalf("sensors: %v, %v unread and %d enclosures; want the one and nothing amiss", err, got.Unread, len(got.Enclosures))
	}

	held := part(t, &got.Enclosures[0], "temperature_sensor", "Backplane").Limits
	if held == nil || pointer.From(held.HighCritical) != 50 || pointer.From(held.HighWarning) != 46 || pointer.From(held.LowWarning) != 10 || held.LowCritical == nil || *held.LowCritical != 0 {
		t.Errorf("the backplane's sensor is held to %+v, want 50, 46, 10 and 0", held)
	}
	if fan := part(t, &got.Enclosures[0], "cooling", "Fan1"); fan.Limits != nil {
		t.Errorf("a fan is held to %+v in degrees", fan.Limits)
	}

	tree, _, h = machine(t)
	h.noLimits = true
	k := sysfs.New(tree.Root, h.devices())
	for range 3 {
		got, err := k.Sensors(t.Context(), card)
		if err != nil || got.Unread != nil || got.Unavailable != "" || len(got.Enclosures) != 1 {
			t.Fatalf("with no page of limits: %v, %v unread, %q unavailable and %d enclosures; want the one and nothing amiss", err, got.Unread, got.Unavailable, len(got.Enclosures))
		}
		if sensor := part(t, &got.Enclosures[0], "temperature_sensor", "Backplane"); sensor.Limits != nil || pointer.From(sensor.Celsius) != 34 {
			t.Errorf("with no page of limits the sensor reads %v and is held to %+v; want 34 and no limits", sensor.Celsius, sensor.Limits)
		}
	}
	if h.limits != 1 {
		t.Errorf("the enclosure was asked for a page it does not have %d times in three rounds, want once", h.limits)
	}
}

// A card says which of its connectors each of its phys is wired to, which it is asked the once, and how many times each link has changed: its own
// phys' and those of the expander it has found, which goes by the kernel's name for it. An expander the kernel does not have is left out.
func TestWhatACardSaysOfItsLinks(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	backplane := sysfstest.Address("0x5000000000000002")
	h.pages = map[uint32]map[mpt.Page][]byte{0: {
		mpt.Manufacturing7: manufacturing7("C0.0", "C0.0", "C0.1", "C0.1"),
		mpt.PhyPage(0):     phyPage(0), mpt.PhyPage(1): phyPage(0), mpt.PhyPage(2): phyPage(3), mpt.PhyPage(3): phyPage(0),
		mpt.NextExpanderPage(0xffff): expanderPage(backplane, 9, 2, 300),
		mpt.ExpanderPhyPage(9, 0):    expanderPhyPage(0, 1),
		mpt.ExpanderPhyPage(9, 1):    expanderPhyPage(1, 255),
		mpt.NextExpanderPage(9):      expanderPage([8]byte{0x50, 0, 0, 0, 0, 0, 0, 0x99}, 10, 1, 1),
		mpt.ExpanderPhyPage(10, 0):   expanderPhyPage(0, 1),
	}}

	k := sysfs.New(tree.Root, h.devices())
	for range 2 {
		got, err := k.Sensors(t.Context(), card)
		if err != nil || got.Unread != nil || got.Unavailable != "" {
			t.Fatalf("sensors: %v, %v unread and %q unavailable; want nothing amiss", err, got.Unread, got.Unavailable)
		}

		connectors := []poll.CardConnector{{Phy: 0, Connector: "C0.0"}, {Phy: 1, Connector: "C0.0"}, {Phy: 2, Connector: "C0.1"}, {Phy: 3, Connector: "C0.1"}}
		if !slices.Equal(got.Connectors, connectors) {
			t.Errorf("connectors = %+v, want %+v", got.Connectors, connectors)
		}

		changes := []poll.LinkChanges{{Phy: 0}, {Phy: 1}, {Phy: 2, Count: 3}, {Phy: 3}, {Expander: "expander-1:0", Phy: 0, Count: 1}, {Expander: "expander-1:0", Phy: 1, Count: 255}}
		if !slices.Equal(got.Changes, changes) || !slices.Equal(got.ExpanderChanges, []poll.ExpanderChanges{{Expander: "expander-1:0", Count: 300}}) {
			t.Errorf("changes = %+v and %+v, want %+v and expander-1:0 at 300", got.Changes, got.ExpanderChanges, changes)
		}
	}

	if h.cardAsks[mpt.Manufacturing7] != 1 || h.cardAsks[mpt.PhyPage(2)] != 2 {
		t.Errorf("the card was asked for its connectors %d times and for a phy %d times in two rounds, want once and twice", h.cardAsks[mpt.Manufacturing7], h.cardAsks[mpt.PhyPage(2)])
	}
}

// A card that fails when asked how often its links have changed is asked nothing more, its temperature included, until the program is started
// again; what it said before it failed is still reported.
func TestACardThatFailsPartWayIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	tree, _, h := machine(t)
	k := sysfs.New(tree.Root, sysfs.Devices{CardPage: func(_ context.Context, _ string, _ uint32, page mpt.Page) ([]byte, error) {
		h.asked = append(h.asked, page.String())
		if page == mpt.PhyPage(1) {
			return nil, errors.New("the card did not answer in 10 seconds, and its driver resets a card that does not")
		}

		return h.cards[0], nil
	}})

	got, err := k.Sensors(t.Context(), card)
	if err != nil || got.Unread == nil || !strings.Contains(got.Unread.Error(), "how often the card's links have changed") || len(got.Temperatures) != 2 {
		t.Fatalf("the first round: %v, %v unread and %d temperatures; want the failure said and the temperatures kept", err, got.Unread, len(got.Temperatures))
	}

	asked := len(h.asked)
	again, _ := k.Sensors(t.Context(), card)
	if len(h.asked) != asked || !strings.Contains(again.Unavailable, "not asked again") {
		t.Errorf("the next round asked the card %d more times and said %q; want nothing asked, and why", len(h.asked)-asked, again.Unavailable)
	}
}
