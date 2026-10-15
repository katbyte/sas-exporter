package readings_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/go-kt/pointer"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/metricstest"
	"github.com/katbyte/go-kt/prometheus/poller"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/readings"
	"github.com/prometheus/client_golang/prometheus"
)

// The names the kernel gives the things on the made-up card.
const (
	backplane = "expander-1:0"
	shelf     = "1:0:24:0"
)

// fixed is a machine whose cards are in whatever state the test says.
type fixed poll.State

func (f fixed) State() poll.State { return poll.State(f) }

// answered is an answer given now.
func answered[T any](value T) poll.Answer[T] {
	return poll.Answer[T]{Value: value, At: time.Now()}
}

// asked is a card the kernel has, asked about its links once with that answer.
func asked(links poll.Links) poll.Card {
	return poll.Card{Name: "host1", Called: "9500-16i", Present: true, HBA: answered(poll.HBA{Name: "host1", Running: true}), Links: answered(links), LinksRound: poll.RoundState{Ran: true, OK: true, LastSuccess: time.Now()}}
}

// internal is a card that has answered everything: four lanes to a backplane, with a few errors counted at the backplane's end of two of them; a disk
// in slot 0 and one in slot 1 that the kernel says less of; an empty slot whose phy has no link; a phy that keeps no counts; and a shelf of four
// slots, one of them failed with its fault light on and one being pointed out.
func internal() poll.Card {
	now := time.Now()

	phys := []poll.Phy{
		{Number: 0, Link: poll.LinkUp, Rate: 12, Maximum: 12, Minimum: 3, HardwareMaximum: 12, HardwareMinimum: 1.5, Attached: poll.AttachedExpander, Errors: &poll.PhyErrors{}},
		{Number: 1, Link: poll.LinkUp, Rate: 12, Maximum: 12, Minimum: 3, HardwareMaximum: 12, HardwareMinimum: 1.5, Attached: poll.AttachedExpander, Errors: &poll.PhyErrors{}},
		{Number: 2, Link: poll.LinkDown, Maximum: 12, Attached: poll.AttachedNone, Errors: &poll.PhyErrors{}},
		{Expander: backplane, Number: 0, Link: poll.LinkUp, Rate: 12, Maximum: 12, Attached: poll.AttachedDevice, Errors: &poll.PhyErrors{}},
		{Expander: backplane, Number: 1, Link: poll.LinkUp, Rate: 6, Maximum: 6, Minimum: 3, HardwareMaximum: 12, HardwareMinimum: 3, Attached: poll.AttachedDevice, Errors: &poll.PhyErrors{LossOfDwordSync: 1, ResetProblems: 1}},
		{Expander: backplane, Number: 2, Link: poll.LinkFailed, Maximum: 12, Attached: poll.AttachedNone, Errors: &poll.PhyErrors{}},
		{Expander: backplane, Number: 12, Link: poll.LinkUp, Rate: 12, Maximum: 12, Attached: poll.AttachedUpstream, Errors: &poll.PhyErrors{InvalidDwords: 13, RunningDisparity: 2, LossOfDwordSync: 3}},
		{Expander: backplane, Number: 13, Link: poll.LinkUp, Rate: 12, Maximum: 12, Attached: poll.AttachedUpstream, Errors: &poll.PhyErrors{InvalidDwords: 5}},
		{Expander: backplane, Number: 40, Link: poll.LinkUp, Rate: 12, Maximum: 12, Attached: poll.AttachedDevice, Errors: &poll.PhyErrors{}},
		{Expander: backplane, Number: 43, Link: poll.LinkDown, Attached: poll.AttachedNone},
	}

	devices := []poll.Device{
		{SASAddress: "5000cca000000040", Serial: "Z0000030", Disk: "sdb", WWN: "5000cca000000041", Vendor: "WDC", Model: "WUH722020BL5204", Revision: "C54K", Expander: backplane, Phy: 0, Enclosure: shelf, Slot: "0", State: "running", Sent: pointer.To(uint64(20141104)), Completed: pointer.To(uint64(20141103)), Errors: pointer.To(uint64(5)), Timeouts: pointer.To(uint64(0)), QueueDepth: pointer.To(uint64(64)), Timeout: pointer.To(uint64(30))},
		{SASAddress: "5000c50000000050", Serial: "Z0000031", Disk: "sdc", Vendor: "ATA", Model: "ST4000NM0035", Revision: "SN04", Expander: backplane, Phy: 1, Enclosure: shelf, Slot: "1", State: "offline", Sent: pointer.To(uint64(9000)), Completed: pointer.To(uint64(8990)), Errors: pointer.To(uint64(240)), Timeouts: pointer.To(uint64(7))},
	}

	slots := []poll.Slot{{Number: "0", Status: "ok", Powered: new(true)}, {Number: "1", Status: "critical", Fault: true, Powered: new(true)}, {Number: "2", Status: "not_installed", Locate: true, Powered: new(true)}, {Number: "3", Status: "not_installed", Powered: new(false)}}

	return poll.Card{
		Name:    "host1",
		Called:  "9500-16i",
		Present: true,
		HBA:     answered(poll.HBA{Name: "host1", SASAddress: "500062b000000001", Model: "HBA 9500-16i", Firmware: "32.00.00.00", BIOS: "09.27.00.00", Driver: "mpt3sas", DriverVersion: "48.100.00.00", Running: true, Resets: pointer.To(uint64(2)), Slot: &poll.PCIeLink{Speed: 8, MaximumSpeed: 16, Width: 4, MaximumWidth: 8}, Serial: "C000000001", Assembly: "03-50134-00001", Chip: "SAS3816", NVData: "2001000bh", NVDataDefault: "2001000ah", MPI: "206.3d", InFlight: pointer.To(uint64(37)), Queue: pointer.To(uint64(4336))}),
		Links: answered(poll.Links{
			Expanders: []poll.Expander{{Name: backplane, SASAddress: "5003048000000002", Vendor: "SMC", Product: "SC846-P", Revision: "100b", ChipVendor: "LSI", Chip: "562", ChipRevision: "3"}},
			Phys:      phys,
			Devices:   devices,
		}),
		Enclosures: answered([]poll.Enclosure{{Name: shelf, ID: "5003048000000002", Vendor: "SMC", Product: "SC846-P", Revision: "100b", Expander: backplane, VendorSpecific: "x40-66.16.11.0", Slots: slots}}),
		Sensors: answered(poll.Sensors{
			Temperatures: []poll.Temperature{{Sensor: poll.SensorChip, Celsius: 53}, {Sensor: poll.SensorBoard, Celsius: 41}},
			Speed:        0.5,
			PowerMode:    "reduced",
			Enclosures:   []poll.EnclosureSensors{{Name: shelf, Conditions: poll.Conditions{Critical: true}, Elements: elements(), Lanes: []poll.Lane{{Expander: backplane, Phy: 12, Connector: "ConnectorA"}, {Expander: backplane, Phy: 13, Connector: "ConnectorA"}, {Expander: backplane, Phy: 0, Connector: "Drive Connector 00"}}}},
		}),

		LinksRound:      poll.RoundState{Ran: true, OK: true, Took: 750 * time.Millisecond, LastSuccess: now},
		EnclosuresRound: poll.RoundState{Ran: true, OK: true, Took: 3 * time.Second, LastSuccess: now.Add(-time.Minute), Failures: 2},
		SensorsRound:    poll.RoundState{Ran: true, OK: true, Took: 120 * time.Millisecond, LastSuccess: now},
	}
}

// elements is what the made-up shelf says of the things in it that are not slots: two fans of which one has failed, a temperature, two voltages, a
// current, two power supplies of which one has lost its mains and one is not fitted, and a connector the enclosure gives no name.
func elements() []poll.Element {
	fine := []poll.Problem{{Name: "ac_fail"}, {Name: "dc_fail"}, {Name: "off"}}
	lost := []poll.Problem{{Name: "ac_fail", Set: true}, {Name: "dc_fail"}, {Name: "off"}}

	return []poll.Element{
		{Kind: "cooling", Name: "Fan1", Status: "ok", RPM: pointer.To(5400.0), Locate: new(false)},
		{Kind: "cooling", Name: "Fan2", Status: "critical", Fault: true, PredictedFailure: true, RPM: pointer.To(0.0), Locate: new(false)},
		{Kind: "temperature_sensor", Name: "Backplane", Status: "ok", Celsius: pointer.To(34.0), Limits: &poll.Limits{HighCritical: pointer.To(50.0), HighWarning: pointer.To(46.0), LowWarning: pointer.To(10.0)}},
		{Kind: "voltage_sensor", Name: "5V", Status: "ok", Volts: pointer.To(5.04)},
		{Kind: "voltage_sensor", Name: "12V", Status: "non_critical", Volts: pointer.To(11.2), Limits: &poll.Limits{HighCritical: pointer.To(0.15), HighWarning: pointer.To(0.1), LowWarning: pointer.To(-0.1), LowCritical: pointer.To(-0.15)}},
		{Kind: "current_sensor", Name: "12V", Status: "ok", Amperes: pointer.To(18.5), Limits: &poll.Limits{HighCritical: pointer.To(0.05)}},
		{Kind: "power_supply", Name: "PS1", Status: "ok", Problems: fine, Locate: new(true)},
		{Kind: "power_supply", Name: "PS2", Status: "critical", Fault: true, Problems: lost},
		{Kind: "power_supply", Name: "PS3", Status: "not_installed"},
		{Kind: "sas_connector", Name: "0", Status: "ok"},
	}
}

// gone is a card the kernel had and no longer lists, three rounds on: nothing it last said is still worth showing.
func gone() poll.Card {
	return poll.Card{Name: "host8", Called: "9500-16e", LinksRound: poll.RoundState{Ran: true, Failures: 3}}
}

// write is the page of these cards' readings as a scrape reads it: the readings, and the one line the page adds to say which build wrote it.
func write(t *testing.T, cards ...poll.Card) string {
	t.Helper()

	return page(t, readings.New(fixed{Cards: cards}))
}

// asking is go-kt's poller holding how the asking of these cards went, as the program's own would after asking them: the readings of that are its
// to write, beside the cards' own.
func asking(t *testing.T, cards ...poll.Card) prometheus.Collector {
	t.Helper()

	called := map[string]string{}
	for i := range cards {
		called[cards[i].Name] = cards[i].Called
	}

	p, err := poller.New(poller.Options[poll.HBA]{Namespace: readings.Namespace, Label: "card", Kind: "SAS card", Targets: func(context.Context) ([]poll.HBA, error) { return nil, nil }, Key: func(h poll.HBA) string { return h.Name }, Names: func(map[string]poller.Target[poll.HBA]) map[string]string { return called }})
	if err != nil {
		t.Fatal(err)
	}

	every := map[poll.Round]time.Duration{poll.RoundLinks: time.Minute, poll.RoundEnclosures: 5 * time.Minute, poll.RoundSensors: time.Minute}
	for _, round := range []poll.Round{poll.RoundLinks, poll.RoundEnclosures, poll.RoundSensors} {
		p.Schedule(string(round), every[round])
	}

	for i := range cards {
		c := &cards[i]
		p.SetTarget(poller.Target[poll.HBA]{Key: c.Name, Value: c.HBA.Value, Listed: c.Present, At: c.HBA.At})
		p.SetRound(c.Name, string(poll.RoundLinks), poller.Round(c.LinksRound))
		p.SetRound(c.Name, string(poll.RoundEnclosures), poller.Round(c.EnclosuresRound))
		p.SetRound(c.Name, string(poll.RoundSensors), poller.Round(c.SensorsRound))
	}

	return p
}

// page is the page these collectors write.
func page(t *testing.T, collectors ...prometheus.Collector) string {
	t.Helper()

	p, err := metrics.New(metrics.Options{Name: "sas-exporter"}, collectors...)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := p.Write(&out); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

// lines is the page's readings without the comments above each, and without the one line that changes with the Go it was built by.
func lines(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "sas_exporter_build_info") {
			out = append(out, line)
		}
	}

	return out
}

// has says whether the page holds a reading that starts as given: its name, or its name and labels.
func has(text, start string) bool {
	for _, line := range lines(text) {
		if strings.HasPrefix(line, start) {
			return true
		}
	}

	return false
}

// expect fails the test for each of these readings the page lacks.
func expect(t *testing.T, text string, wanted ...string) {
	t.Helper()

	for _, want := range wanted {
		if !has(text, want) {
			t.Errorf("the page lacks %s:\n%s", want, text)
		}
	}
}

// The page is what dashboards and alerts are written against, so the whole of it, the poller's readings of how the asking went included, is kept
// in testdata/page.txt and any change to a name, a label or a help text shows up as a difference there: go test ./lib/readings -update rewrites it
// once the change is meant. It is written at the start of the year 2000, where the test's clock begins.
func TestThePageIsWhatWasSaved(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cards := []poll.Card{internal(), gone()}
		metricstest.Saved(t, page(t, readings.New(fixed{Cards: cards}), asking(t, cards...)), "testdata/page.txt")
	})
}

// The names follow the conventions Prometheus checks for: a unit at the end, _total on what only goes up, help on everything. The one exception
// is a SAS link's speed, which the check would have in bytes: it is in gigabits a second here, as a SAS link's speed is always given.
func TestThePageFollowsTheNamingConventions(t *testing.T) {
	t.Parallel()

	cards := []poll.Card{internal(), gone()}
	metricstest.Lint(t, page(t, readings.New(fixed{Cards: cards}), asking(t, cards...)), metricstest.Allowed{Reading: "_gigabits_per_second", Problem: `instead of "gigabits"`})
}

// Every reading says which card it hangs off, which is what lets a rise in errors be pinned to one card or the other.
func TestEveryReadingSaysWhichCard(t *testing.T) {
	t.Parallel()

	for _, line := range lines(write(t, internal(), gone())) {
		if !strings.Contains(line, `card="9500-16i"`) && !strings.Contains(line, `card="9500-16e"`) {
			t.Errorf("a reading does not say which card it is of: %s", line)
		}
	}
}

// A card that has never been asked about has no readings at all, and one the kernel no longer lists has only that it is down and how the asking
// went: a reading of no disks would say something nobody knows.
func TestACardThatNeverAnsweredHasNoReadingsOfItsState(t *testing.T) {
	t.Parallel()

	if got := lines(write(t, poll.Card{Name: "host1", Called: "9500-16i"})); len(got) != 0 {
		t.Errorf("a card never asked about has readings: %v", got)
	}

	// how the asking went is the poller's to write, beside these
	want := []string{`sas_card_up{card="9500-16e"} 0`}
	if got := lines(write(t, gone())); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("a card that is gone reads\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A card the kernel is resetting is down, while what it last said of itself and what hangs off it stay on the page: those are still the newest known.
func TestACardBeingResetIsDown(t *testing.T) {
	t.Parallel()

	card := internal()
	card.HBA.Value.Running = false

	expect(t, write(t, card), `sas_card_up{card="9500-16i"} 0`, `sas_card_info{assembly="03-50134-00001",bios="09.27.00.00",card="9500-16i",chip="SAS3816",driver="mpt3sas",driver_version="48.100.00.00",firmware="32.00.00.00",model="HBA 9500-16i",mpi="206.3d",nvdata="2001000bh",nvdata_default="2001000ah",sas_address="500062b000000001",scsi_host="host1",serial="C000000001"} 1`, `sas_card_resets_total{card="9500-16i"} 2`, `sas_phy_link_state{card="9500-16i",expander="",phy="0",state="up"} 1`)
}

// What each phy says of its link: its state and what is on the other end as labels, its speed in gigabits a second as SAS speeds are always
// given, 0 with no link since that is what it is running at, and the most it is set to offer, left out for a phy that does not say.
func TestWhatAPhySaysOfItsLink(t *testing.T) {
	t.Parallel()

	got := write(t, internal())
	expect(t, got,
		`sas_phy_info{attached="expander",card="9500-16i",expander="",phy="0"} 1`,
		`sas_phy_info{attached="upstream",card="9500-16i",expander="expander-1:0",phy="12"} 1`,
		`sas_phy_info{attached="device",card="9500-16i",expander="expander-1:0",phy="1"} 1`,
		`sas_phy_info{attached="none",card="9500-16i",expander="expander-1:0",phy="2"} 1`,
		`sas_phy_link_state{card="9500-16i",expander="expander-1:0",phy="1",state="up"} 1`,
		`sas_phy_link_state{card="9500-16i",expander="expander-1:0",phy="2",state="failed"} 1`,
		`sas_phy_link_state{card="9500-16i",expander="",phy="2",state="down"} 1`,
		`sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="1"} 6`,
		`sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="12"} 12`,
		`sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="2"} 0`,
		`sas_phy_maximum_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="1"} 6`,
		`sas_phy_hardware_maximum_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="1"} 12`,
		`sas_phy_minimum_link_rate_gigabits_per_second{card="9500-16i",expander="",phy="0"} 3`,
		`sas_phy_hardware_minimum_link_rate_gigabits_per_second{card="9500-16i",expander="",phy="0"} 1.5`,
		`sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="43"} 0`,
	)

	for _, limit := range []string{"maximum", "minimum", "hardware_maximum", "hardware_minimum"} {
		if has(got, `sas_phy_`+limit+`_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="43"}`) {
			t.Errorf("a phy that does not say its %s speed has a reading of it:\n%s", limit, got)
		}
	}
}

// A phy's four error counts are one reading by kind, so one question covers them all, and each is on the page at 0 until it counts something: a
// count that appears only once it is not 0 cannot be seen to have grown. A phy that keeps no counts has none.
func TestAPhysErrorCounts(t *testing.T) {
	t.Parallel()

	got := write(t, internal())
	expect(t, got,
		`sas_phy_errors_total{card="9500-16i",error="invalid_dword",expander="expander-1:0",phy="12"} 13`,
		`sas_phy_errors_total{card="9500-16i",error="running_disparity",expander="expander-1:0",phy="12"} 2`,
		`sas_phy_errors_total{card="9500-16i",error="loss_of_dword_sync",expander="expander-1:0",phy="12"} 3`,
		`sas_phy_errors_total{card="9500-16i",error="phy_reset_problem",expander="expander-1:0",phy="12"} 0`,
		`sas_phy_errors_total{card="9500-16i",error="phy_reset_problem",expander="expander-1:0",phy="1"} 1`,
		`sas_phy_errors_total{card="9500-16i",error="invalid_dword",expander="",phy="0"} 0`,
	)

	if has(got, `sas_phy_errors_total{card="9500-16i",error="invalid_dword",expander="expander-1:0",phy="43"}`) {
		t.Errorf("a phy that keeps no error counts has a reading of them:\n%s", got)
	}
}

// A disk goes by its serial number, which is what joins it to what other tools say of the same disk, with where it is on the one reading that
// ties a serial number to a slot and to the phy its link is on.
func TestADisk(t *testing.T) {
	t.Parallel()

	expect(t, write(t, internal()),
		`sas_device_info{card="9500-16i",disk="sdb",enclosure="1:0:24:0",expander="expander-1:0",model="WUH722020BL5204",phy="0",revision="C54K",sas_address="5000cca000000040",serial="Z0000030",slot="0",vendor="WDC",wwn="5000cca000000041"} 1`,
		`sas_device_state{card="9500-16i",disk="sdb",serial="Z0000030",state="running"} 1`,
		`sas_device_state{card="9500-16i",disk="sdc",serial="Z0000031",state="offline"} 1`,
		`sas_device_command_errors_total{card="9500-16i",disk="sdc",serial="Z0000031"} 240`,
		`sas_device_command_timeouts_total{card="9500-16i",disk="sdc",serial="Z0000031"} 7`,
		`sas_device_command_timeouts_total{card="9500-16i",disk="sdb",serial="Z0000030"} 0`,
		`sas_device_commands_total{card="9500-16i",disk="sdb",serial="Z0000030"} 2.0141104e+07`,
		`sas_device_commands_completed_total{card="9500-16i",disk="sdb",serial="Z0000030"} 2.0141103e+07`,
		`sas_device_queue_depth{card="9500-16i",disk="sdb",serial="Z0000030"} 64`,
		`sas_device_timeout_seconds{card="9500-16i",disk="sdb",serial="Z0000030"} 30`,
	)
}

// A disk the kernel says little of is still on the page under what it does say, and what it does not say is left off rather than written as nothing
// or as 0: no counts on a kernel that keeps none, no phy when its port lists none.
func TestADiskTheKernelSaysLittleOf(t *testing.T) {
	t.Parallel()

	got := write(t, asked(poll.Links{Devices: []poll.Device{{SASAddress: "5000cca000000040", Phy: -1}}}))
	expect(t, got, `sas_device_info{card="9500-16i",disk="",enclosure="",expander="",model="",phy="",revision="",sas_address="5000cca000000040",serial="",slot="",vendor="",wwn=""} 1`)

	for _, absent := range []string{"sas_device_state", "sas_device_commands_total", "sas_device_commands_completed_total", "sas_device_command_errors_total", "sas_device_command_timeouts_total", "sas_device_queue_depth", "sas_device_timeout_seconds"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s the kernel never gave:\n%s", absent, got)
		}
	}
}

// What an enclosure says of each slot: its state as a label, and its two lights and its power as a yes or no each, so an empty slot and a failed
// one can be told apart, a light left on can be found, and so can a slot the enclosure has turned off.
func TestAnEnclosuresSlots(t *testing.T) {
	t.Parallel()

	expect(t, write(t, internal()),
		`sas_enclosure_info{card="9500-16i",enclosure="1:0:24:0",expander="expander-1:0",id="5003048000000002",product="SC846-P",revision="100b",vendor="SMC",vendor_specific="x40-66.16.11.0"} 1`,
		`sas_enclosure_slots{card="9500-16i",enclosure="1:0:24:0"} 4`,
		`sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:24:0",slot="0",status="ok"} 1`,
		`sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:24:0",slot="1",status="critical"} 1`,
		`sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:24:0",slot="3",status="not_installed"} 1`,
		`sas_enclosure_slot_fault{card="9500-16i",enclosure="1:0:24:0",slot="1"} 1`,
		`sas_enclosure_slot_fault{card="9500-16i",enclosure="1:0:24:0",slot="0"} 0`,
		`sas_enclosure_slot_locate{card="9500-16i",enclosure="1:0:24:0",slot="2"} 1`,
		`sas_enclosure_slot_locate{card="9500-16i",enclosure="1:0:24:0",slot="1"} 0`,
		`sas_enclosure_slot_powered{card="9500-16i",enclosure="1:0:24:0",slot="0"} 1`,
		`sas_enclosure_slot_powered{card="9500-16i",enclosure="1:0:24:0",slot="3"} 0`,
	)
}

// Two readings with the same labels would lose the whole page, so a thing the kernel lists twice is written once: a phy, an expander, a slot, an
// enclosure, or two disks that both give no serial number and no name.
func TestAThingListedTwiceIsWrittenOnce(t *testing.T) {
	t.Parallel()

	card := asked(poll.Links{
		Expanders: []poll.Expander{{Name: backplane, Product: "SC846-P"}, {Name: backplane, Product: "SC826N4"}},
		Phys:      []poll.Phy{{Number: 0, Link: poll.LinkUp, Rate: 12}, {Number: 0, Link: poll.LinkDown}, {Expander: backplane, Number: 0, Link: poll.LinkUp, Rate: 6}},
		Devices:   []poll.Device{{SASAddress: "5000cca000000040"}, {SASAddress: "5000cca000000044"}, {SASAddress: "5000cca000000048", Serial: "Z0000030"}},
	})
	card.Enclosures = answered([]poll.Enclosure{{Name: shelf, Slots: []poll.Slot{{Number: "0", Status: "ok"}, {Number: "0", Status: "critical"}}}, {Name: shelf}})

	got := write(t, card)
	for start, want := range map[string]int{"sas_expander_info": 1, "sas_phy_link_state": 2, "sas_phy_link_rate_gigabits_per_second": 2, "sas_device_info": 2, "sas_enclosure_info": 1, "sas_enclosure_slot_status": 1} {
		var n int
		for _, line := range lines(got) {
			if strings.HasPrefix(line, start+"{") {
				n++
			}
		}
		if n != want {
			t.Errorf("%d readings of %s, want %d:\n%s", n, start, want, got)
		}
	}

	// the first of two is the one kept
	expect(t, got, `sas_phy_link_state{card="9500-16i",expander="",phy="0",state="up"} 1`, `sas_expander_info{card="9500-16i",chip="",chip_revision="",chip_vendor="",expander="expander-1:0",product="SC846-P"`, `sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:24:0",slot="0",status="ok"} 1`)
}

// A card's own link to the machine: how fast and how wide it came up, beside the most it could be, so a card in the wrong slot shows as a link
// slower or narrower than it could have. What the kernel does not know is left off rather than given as 0, which would be a link that is down.
func TestACardsOwnLinkToTheMachine(t *testing.T) {
	t.Parallel()

	got := write(t, internal())
	expect(t, got,
		`sas_card_pcie_link_speed_gigatransfers_per_second{card="9500-16i"} 8`,
		`sas_card_pcie_maximum_link_speed_gigatransfers_per_second{card="9500-16i"} 16`,
		`sas_card_pcie_link_width{card="9500-16i"} 4`,
		`sas_card_pcie_maximum_link_width{card="9500-16i"} 8`,
		`sas_card_commands_in_flight{card="9500-16i"} 37`,
		`sas_card_commands_in_flight_limit{card="9500-16i"} 4336`,
	)

	card := internal()
	card.HBA.Value.Slot = &poll.PCIeLink{Width: 8}
	got = write(t, card)
	if !has(got, `sas_card_pcie_link_width{card="9500-16i"} 8`) || has(got, "sas_card_pcie_link_speed") || has(got, "sas_card_pcie_maximum") {
		t.Errorf("a link of which only the width is known should have that and nothing else:\n%s", got)
	}

	card.HBA.Value.Slot = nil
	if has(write(t, card), "sas_card_pcie") {
		t.Error("a card the kernel says nothing of the kind about has a link on the page")
	}
}

// What the hardware measures of itself, in the units each is always given in: the card's temperatures by where they are taken, and for each
// enclosure its fans in turns a minute, its temperatures in degrees, its voltages in volts and its currents in amps. The limits a voltage or a
// current is held to are the share of what it should read that the enclosure gives, since it does not say what that is.
func TestWhatTheHardwareMeasures(t *testing.T) {
	t.Parallel()

	expect(t, write(t, internal()),
		`sas_card_temperature_celsius{card="9500-16i",sensor="chip"} 53`,
		`sas_card_speed_ratio{card="9500-16i"} 0.5`,
		`sas_phy_connector_info{card="9500-16i",connector="ConnectorA",enclosure="1:0:24:0",expander="expander-1:0",phy="12"} 1`,
		`sas_phy_connector_info{card="9500-16i",connector="Drive Connector 00",enclosure="1:0:24:0",expander="expander-1:0",phy="0"} 1`,
		`sas_card_power_mode{card="9500-16i",mode="reduced"} 1`,
		`sas_card_power_mode{card="9500-16i",mode="full"} 0`,
		`sas_card_temperature_celsius{card="9500-16i",sensor="board"} 41`,
		`sas_enclosure_fan_speed_rpm{card="9500-16i",element="Fan1",enclosure="1:0:24:0"} 5400`,
		`sas_enclosure_fan_speed_rpm{card="9500-16i",element="Fan2",enclosure="1:0:24:0"} 0`,
		`sas_enclosure_temperature_celsius{card="9500-16i",element="Backplane",enclosure="1:0:24:0"} 34`,
		`sas_enclosure_temperature_limit_celsius{card="9500-16i",element="Backplane",enclosure="1:0:24:0",limit="high_critical"} 50`,
		`sas_enclosure_temperature_limit_celsius{card="9500-16i",element="Backplane",enclosure="1:0:24:0",limit="high_warning"} 46`,
		`sas_enclosure_temperature_limit_celsius{card="9500-16i",element="Backplane",enclosure="1:0:24:0",limit="low_warning"} 10`,
		`sas_enclosure_voltage_volts{card="9500-16i",element="5V",enclosure="1:0:24:0"} 5.04`,
		`sas_enclosure_voltage_volts{card="9500-16i",element="12V",enclosure="1:0:24:0"} 11.2`,
		`sas_enclosure_current_amperes{card="9500-16i",element="12V",enclosure="1:0:24:0"} 18.5`,
		`sas_enclosure_voltage_limit_ratio{card="9500-16i",element="12V",enclosure="1:0:24:0",limit="high_critical"} 0.15`,
		`sas_enclosure_voltage_limit_ratio{card="9500-16i",element="12V",enclosure="1:0:24:0",limit="low_critical"} -0.15`,
		`sas_enclosure_current_limit_ratio{card="9500-16i",element="12V",enclosure="1:0:24:0",limit="high_critical"} 0.05`,
	)

	// a limit the enclosure does not set is left off, not written as a limit of nothing
	got := write(t, internal())
	for _, absent := range []string{`sas_enclosure_temperature_limit_celsius{card="9500-16i",element="Backplane",enclosure="1:0:24:0",limit="low_critical"}`, `sas_enclosure_current_limit_ratio{card="9500-16i",element="12V",enclosure="1:0:24:0",limit="high_warning"}`} {
		if has(got, absent) {
			t.Errorf("a limit that is not set is on the page: %s\n%s", absent, got)
		}
	}
}

// Everything in an enclosure that is not a slot has what the enclosure says of it and whether it has failed, whatever kind of thing it is, so one
// question finds anything wrong. A power supply also says which of the ways it can be in trouble it is in, and an enclosure sums itself up.
func TestWhatAnEnclosureSaysOfWhatIsInIt(t *testing.T) {
	t.Parallel()

	got := write(t, internal())
	expect(t, got,
		`sas_enclosure_element_status{card="9500-16i",element="Fan1",enclosure="1:0:24:0",status="ok",type="cooling"} 1`,
		`sas_enclosure_element_status{card="9500-16i",element="Fan2",enclosure="1:0:24:0",status="critical",type="cooling"} 1`,
		`sas_enclosure_element_status{card="9500-16i",element="PS3",enclosure="1:0:24:0",status="not_installed",type="power_supply"} 1`,
		`sas_enclosure_element_status{card="9500-16i",element="0",enclosure="1:0:24:0",status="ok",type="sas_connector"} 1`,
		// a voltage sensor and a current sensor of one name are told apart by their kind
		`sas_enclosure_element_status{card="9500-16i",element="12V",enclosure="1:0:24:0",status="non_critical",type="voltage_sensor"} 1`,
		`sas_enclosure_element_status{card="9500-16i",element="12V",enclosure="1:0:24:0",status="ok",type="current_sensor"} 1`,
		`sas_enclosure_element_fault{card="9500-16i",element="Fan2",enclosure="1:0:24:0",type="cooling"} 1`,
		`sas_enclosure_element_fault{card="9500-16i",element="Fan1",enclosure="1:0:24:0",type="cooling"} 0`,
		`sas_enclosure_element_predicted_failure{card="9500-16i",element="Fan2",enclosure="1:0:24:0",type="cooling"} 1`,
		`sas_enclosure_element_predicted_failure{card="9500-16i",element="Fan1",enclosure="1:0:24:0",type="cooling"} 0`,
		`sas_enclosure_element_locate{card="9500-16i",element="PS1",enclosure="1:0:24:0",type="power_supply"} 1`,
		`sas_enclosure_element_locate{card="9500-16i",element="Fan1",enclosure="1:0:24:0",type="cooling"} 0`,
		`sas_enclosure_element_fault{card="9500-16i",element="PS2",enclosure="1:0:24:0",type="power_supply"} 1`,
		`sas_enclosure_power_supply_problem{card="9500-16i",element="PS2",enclosure="1:0:24:0",problem="ac_fail"} 1`,
		`sas_enclosure_power_supply_problem{card="9500-16i",element="PS2",enclosure="1:0:24:0",problem="dc_fail"} 0`,
		`sas_enclosure_power_supply_problem{card="9500-16i",element="PS1",enclosure="1:0:24:0",problem="ac_fail"} 0`,
		`sas_enclosure_condition{card="9500-16i",condition="critical",enclosure="1:0:24:0"} 1`,
		`sas_enclosure_condition{card="9500-16i",condition="non_critical",enclosure="1:0:24:0"} 0`,
		`sas_enclosure_condition{card="9500-16i",condition="unrecoverable",enclosure="1:0:24:0"} 0`,
		`sas_enclosure_condition{card="9500-16i",condition="info",enclosure="1:0:24:0"} 0`,
	)

	// a power supply that is not fitted is in no particular trouble, a thing that measures nothing has no measurement, and one that has no locate
	// light has no reading of it
	for _, absent := range []string{`sas_enclosure_power_supply_problem{element="PS3"`, `sas_enclosure_fan_speed_rpm{element="PS1"`, `sas_enclosure_temperature_celsius{element="Fan1"`, `sas_enclosure_element_locate{card="9500-16i",element="0"`} {
		if has(got, absent) {
			t.Errorf("the page has %s, which is not something that thing has:\n%s", absent, got)
		}
	}
}

// Where there was no leave to ask the hardware, nothing of what it measures is on the page, and the rest is as it would be anyway. That the round
// is not said to have run is the poller's part, tested with the poller.
func TestWithNoLeaveToAskTheHardware(t *testing.T) {
	t.Parallel()

	card := internal()
	card.Sensors, card.SensorsRound = poll.Answer[poll.Sensors]{}, poll.RoundState{}

	got := write(t, card)
	for _, absent := range []string{"sas_card_temperature_celsius", "sas_enclosure_element", "sas_enclosure_condition", "sas_enclosure_fan", "sas_enclosure_temperature"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s with no leave to read it:\n%s", absent, got)
		}
	}
	expect(t, got, `sas_card_up{card="9500-16i"} 1`, `sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:24:0",slot="0",status="ok"} 1`)
}
