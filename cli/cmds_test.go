package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/poll/polltest"
	"github.com/katbyte/sas-exporter/lib/sysfs"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
	"github.com/spf13/cobra"
)

// The two cards the made-up kernel has.
const (
	inside  = "host1"
	outside = "host8"
)

// kernel is a connector that hands out this kernel wherever its /sys is said to be.
func kernel(k *polltest.Kernel) connector {
	return func(source) (poll.Kernel, error) { return k, nil }
}

// twoCards is a kernel with both cards in service, three phys on the first and a shelf of two slots on the second.
func twoCards() *polltest.Kernel {
	return &polltest.Kernel{
		Cards:            []poll.HBA{polltest.Card(inside), polltest.Card(outside)},
		LinksAnswer:      map[string]poll.Links{inside: {Phys: []poll.Phy{{Number: 0, Link: poll.LinkUp, Rate: 12}, {Number: 1, Link: poll.LinkUp, Rate: 12}, {Number: 2, Link: poll.LinkDown}}}},
		EnclosuresAnswer: map[string][]poll.Enclosure{outside: {{Name: "8:0:24:0", Slots: []poll.Slot{{Number: "0", Status: "ok"}, {Number: "1", Status: "not_installed"}}}}},
	}
}

// program is the program given these arguments and this kernel, not yet run, and where what it prints will go.
func program(t *testing.T, connect connector, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	root, err := makeWith(connect)
	if err != nil {
		t.Fatalf("makeWith: %v", err)
	}

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)

	return root, &out
}

// run executes the program with these arguments against this kernel, and gives back what it printed.
func run(t *testing.T, connect connector, args ...string) (string, error) {
	t.Helper()

	root, out := program(t, connect, args...)
	err := root.ExecuteContext(t.Context())

	return out.String(), err
}

// count is how many readings of that name the page holds.
func count(page, name string) int {
	var n int
	for line := range strings.Lines(page) {
		if strings.HasPrefix(line, name+"{") {
			n++
		}
	}

	return n
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestMakeBuildsTheCommands(t *testing.T) {
	alone(t)

	root, err := Make()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"version", "print", "serve"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("no %s command: %v", name, err)
		}
	}
	// a setting is a flag on every command, so it can be given after whichever one is run
	for _, name := range []string{"sysfs", "dev", "listen", "labels", "sensors", "card-names", "intervals", "config"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("no --%s flag", name)
		}
	}
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestVersionCommand(t *testing.T) {
	alone(t)

	out, err := run(t, kernel(twoCards()), "version")
	if err != nil || !strings.HasPrefix(out, "sas-exporter ") {
		t.Errorf("version printed %q (%v), want the name and a version", out, err)
	}
}

// With no command the program says where the help is rather than serving or failing.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestNoCommandPointsAtHelp(t *testing.T) {
	alone(t)

	out, err := run(t, kernel(twoCards()))
	if err != nil || !strings.Contains(out, "sas-exporter help") {
		t.Errorf("no command printed %q (%v), want a pointer to help", out, err)
	}
}

// print asks every question once and writes the page a scrape would read, both cards on it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintWritesThePage(t *testing.T) {
	alone(t)

	k := twoCards()
	out, err := run(t, kernel(k), "print")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`sas_card_up{card="host1"} 1`,
		`sas_card_up{card="host8"} 1`,
		`sas_phy_link_rate_gigabits_per_second{card="host1",expander="",phy="0"} 12`,
		`sas_phy_link_state{card="host1",expander="",phy="2",state="down"} 1`,
		`sas_enclosure_slots{card="host8",enclosure="8:0:24:0"} 2`,
		`sas_enclosure_slot_status{card="host8",enclosure="8:0:24:0",slot="1",status="not_installed"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s:\n%s", want, out)
		}
	}
	// once each, not on a schedule
	if k.Asked(polltest.Of(polltest.Links, inside)) != 1 || k.Asked(polltest.Of(polltest.Enclosures, outside)) != 1 {
		t.Errorf("print asked for a card's links %d times and its enclosures %d, want once each", k.Asked(polltest.Of(polltest.Links, inside)), k.Asked(polltest.Of(polltest.Enclosures, outside)))
	}
}

// The program whole, reading a real machine's /sys from files as it would on that machine: every card, expander, phy, disk and slot it has is on
// the page, and the phys whose error counts the expanders keep to themselves are there without them. Its devices are looked for in a directory
// with none in it, as every test's are: a test must never ask the hardware of whatever machine it happens to run on. That is also what a run
// without leave to open them looks like, so the page has none of what the hardware measures and print still succeeds.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintReadsARealMachine(t *testing.T) {
	alone(t)

	root, _ := sysfstest.NAS(t)
	out, err := run(t, connect, "print", "--sysfs", root, "--dev", t.TempDir())
	if err != nil {
		t.Fatalf("print: %v", err)
	}

	for name, want := range map[string]int{
		"sas_card_up":                                            sysfstest.NASCards,
		"sas_card_info":                                          sysfstest.NASCards,
		"sas_expander_info":                                      sysfstest.NASExpanders,
		"sas_phy_info":                                           sysfstest.NASPhys,
		"sas_phy_link_state":                                     sysfstest.NASPhys,
		"sas_phy_errors_total":                                   4 * (sysfstest.NASPhys - sysfstest.NASRefused),
		"sas_device_info":                                        sysfstest.NASDisks,
		"sas_device_command_errors_total":                        sysfstest.NASDisks,
		"sas_device_commands_total":                              sysfstest.NASDisks,
		"sas_card_pcie_link_width":                               sysfstest.NASCards,
		"sas_enclosure_info":                                     sysfstest.NASEnclosures,
		"sas_enclosure_slot_status":                              sysfstest.NASSlots,
		"sas_enclosure_slot_powered":                             sysfstest.NASSlots,
		"sas_card_commands_in_flight":                            sysfstest.NASCards,
		"sas_card_commands_in_flight_limit":                      sysfstest.NASCards,
		"sas_device_queue_depth":                                 sysfstest.NASDisks,
		"sas_device_timeout_seconds":                             sysfstest.NASDisks,
		"sas_phy_minimum_link_rate_gigabits_per_second":          sysfstest.NASPhys - sysfstest.NASSpeedless,
		"sas_phy_hardware_maximum_link_rate_gigabits_per_second": sysfstest.NASPhys - sysfstest.NASSpeedless,
		"sas_phy_hardware_minimum_link_rate_gigabits_per_second": sysfstest.NASPhys - sysfstest.NASSpeedless,
		"sas_card_temperature_celsius":                           0,
		"sas_enclosure_element_status":                           0,
		// the round that asks the hardware had nothing it could ask, so it is not said to have run
		"sas_poll_failures_total": 2 * sysfstest.NASCards,
	} {
		if got := count(out, name); got != want {
			t.Errorf("%d readings of %s, want %d", got, name, want)
		}
	}

	for _, want := range []string{
		`sas_card_info{assembly="03-50134-00001",bios="09.27.00.00",card="9500-16i",chip="SAS3816",driver="mpt3sas",driver_version="48.100.00.00",firmware="32.00.00.00",model="HBA 9500-16i",mpi="206.3d",nvdata="32.01.00.11",nvdata_default="32.01.00.11",sas_address="500062b000000001",scsi_host="host1",serial="C000000001"} 1`,
		`sas_expander_info{card="9500-16e",chip="563",chip_revision="3",chip_vendor="LSI",expander="expander-8:1",product="SC847-P",revision="100b",sas_address="5003048000000063",vendor="SMC"} 1`,
		`sas_phy_info{attached="upstream",card="9500-16i",expander="expander-1:0",phy="12"} 1`,
		`sas_phy_errors_total{card="9500-16i",error="invalid_dword",expander="expander-1:0",phy="13"} 12`,
		`sas_phy_errors_total{card="9500-16e",error="running_disparity",expander="expander-8:0",phy="21"} 3`,
		`sas_device_info{card="9500-16i",disk="sdb",enclosure="1:0:24:0",expander="expander-1:0",model="WUH722020BL5204",phy="0",revision="C54K",sas_address="5000cca000000040",serial="Z0000030",slot="0",vendor="WDC",wwn="5000cca000000041"} 1`,
		`sas_enclosure_slot_status{card="9500-16i",enclosure="1:0:35:0",slot="11",status="not_installed"} 1`,
		`sas_poll_failures_total{card="9500-16e",poll="links"} 0`,
		`sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="12"} 12`,
		`sas_card_pcie_link_speed_gigatransfers_per_second{card="9500-16e"} 16`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// A card goes by its model on every reading, and by the name chosen for it when one is: by the name it would have had, or by its SAS address,
// which is what tells two cards of one model apart. The kernel's name for it is on the reading that says what the card is. A name written
// wrongly stops both commands before anything is asked.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestCardsGoByTheNamesChosenForThem(t *testing.T) {
	alone(t)

	root, _ := sysfstest.NAS(t)
	out, err := run(t, connect, "print", "--sysfs", root, "--dev", t.TempDir(), "--card-names", "9500-16e=jbod,0x500062b000000001=inside")
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	for _, want := range []string{
		`sas_card_up{card="inside"} 1`,
		`sas_card_up{card="jbod"} 1`,
		`sas_card_info{assembly="03-50134-00001",bios="09.27.00.00",card="inside",chip="SAS3816",driver="mpt3sas",driver_version="48.100.00.00",firmware="32.00.00.00",model="HBA 9500-16i",mpi="206.3d",nvdata="32.01.00.11",nvdata_default="32.01.00.11",sas_address="500062b000000001",scsi_host="host1",serial="C000000001"} 1`,
		`sas_phy_info{attached="upstream",card="jbod",expander="expander-8:0",phy="20"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(out, `card="9500-16`) || strings.Contains(out, `card="host`) {
		t.Errorf("a card still goes by the name it had before one was chosen for it:\n%s", out)
	}

	for _, command := range []string{"print", "serve"} {
		alone(t)

		k := twoCards()
		_, err := run(t, kernel(k), command, "--listen", "127.0.0.1:0", "--card-names", "host1")
		if err == nil || !strings.Contains(err.Error(), "SAS_EXPORTER_CARD_NAMES") {
			t.Errorf("%s --card-names host1 = %v, want an error naming the setting", command, err)
		}
		if asked := k.Asked(polltest.HBAs); asked != 0 {
			t.Errorf("%s --card-names host1 asked the kernel %d questions before refusing", command, asked)
		}
	}
}

// The program whole again, this time with the real machine's enclosures answering as they did when its /sys was saved: every fan, power supply,
// temperature and voltage they reported is on the page beside the rest, under the name the enclosure gave it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintReadsWhatARealMachinesEnclosuresSay(t *testing.T) {
	alone(t)

	root, capture := sysfstest.NAS(t)
	saved := func(from source) (poll.Kernel, error) {
		return sysfs.New(from.sysfs, sysfs.Devices{Diagnostic: capture.Diagnostic}), nil
	}
	out, err := run(t, saved, "print", "--sysfs", root)
	if err != nil {
		t.Fatalf("print: %v", err)
	}

	for name, want := range map[string]int{
		"sas_enclosure_fan_speed_rpm":        sysfstest.NASFans,
		"sas_enclosure_temperature_celsius":  sysfstest.NASTemperatures,
		"sas_enclosure_voltage_volts":        sysfstest.NASVoltages,
		"sas_enclosure_power_supply_problem": 8 * sysfstest.NASSupplies,
		"sas_enclosure_condition":            4 * sysfstest.NASEnclosures,
		"sas_enclosure_slot_status":          sysfstest.NASSlots,
		"sas_card_temperature_celsius":       0,
		"sas_poll_failures_total":            3 * sysfstest.NASCards,
	} {
		if got := count(out, name); got != want {
			t.Errorf("%d readings of %s, want %d", got, name, want)
		}
	}

	for _, want := range []string{
		`sas_enclosure_fan_speed_rpm{card="9500-16e",element="FAN04",enclosure="8:0:24:0"} 5100`,
		`sas_enclosure_temperature_celsius{card="9500-16i",element="ChipDie",enclosure="1:0:35:0"} 71`,
		`sas_enclosure_voltage_volts{card="9500-16e",element="VBAT",enclosure="8:0:25:0"} 2.89`,
		`sas_enclosure_element_status{card="9500-16e",element="PS0000000000002",enclosure="8:0:24:0",status="ok",type="power_supply"} 1`,
		`sas_enclosure_condition{card="9500-16e",condition="critical",enclosure="8:0:24:0"} 1`,
		`sas_poll_failures_total{card="9500-16i",poll="sensors"} 0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// What the hardware measures of itself goes on the page beside the rest when it could be asked. A card there was no leave to ask is not a
// question unanswered, so print still succeeds with the page lacking that card's; one that was asked and would not answer makes print fail.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintWithWhatTheHardwareMeasures(t *testing.T) {
	alone(t)

	k := twoCards()
	k.SensorsAnswer = map[string]poll.Sensors{
		inside:  {Temperatures: []poll.Temperature{{Sensor: poll.SensorChip, Celsius: 53}}, Enclosures: []poll.EnclosureSensors{{Name: "1:0:24:0", Elements: []poll.Element{{Kind: "cooling", Name: "Fan1", Status: "ok"}}}}},
		outside: {Unavailable: "the card's temperature: opening /dev/mpt3ctl: permission denied"},
	}

	out, err := run(t, kernel(k), "print")
	if err != nil {
		t.Fatalf("print with one card that could be asked and one that could not: %v", err)
	}
	for _, want := range []string{`sas_card_temperature_celsius{card="host1",sensor="chip"} 53`, `sas_enclosure_element_status{card="host1",element="Fan1",enclosure="1:0:24:0",status="ok",type="cooling"} 1`, `sas_poll_failures_total{card="host1",poll="sensors"} 0`} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `card="host8",poll="sensors"`) || strings.Contains(out, `sas_card_temperature_celsius{card="host8"`) {
		t.Errorf("the card there was no leave to ask has readings of what it measures, or of that round having run:\n%s", out)
	}

	alone(t)
	k.SensorsAnswer[outside] = poll.Sensors{Unread: errors.New("the card's temperature: no data available")}
	if _, err := run(t, kernel(k), "print"); err == nil || !strings.Contains(err.Error(), outside) {
		t.Errorf("print with a card that would not answer = %v, want an error naming it", err)
	}
}

// Labels asked for are on every reading, the one that says which build wrote the page included, beside the labels a reading has of its own.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestLabelsGoOnEveryReading(t *testing.T) {
	alone(t)

	out, err := run(t, kernel(twoCards()), "print", "--labels", "hostname=nas, site=home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`sas_card_up{card="host1",hostname="nas",site="home"} 1`, `sas_enclosure_slots{card="host8",enclosure="8:0:24:0",hostname="nas",site="home"} 2`, `sas_exporter_build_info{commit="",goversion="`, `hostname="nas",site="home",version="`} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s:\n%s", want, out)
		}
	}
	for line := range strings.Lines(out) {
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" && !strings.Contains(line, `hostname="nas"`) {
			t.Errorf("a reading has no hostname label: %s", line)
		}
	}
}

// A label that cannot be used stops both commands before the kernel is asked anything, and the error says which label and which setting: one not
// written name=value, one a store would not take, and one named as a reading's own label is.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestALabelThatCannotBeUsed(t *testing.T) {
	for labels, want := range map[string]string{"hostname": "hostname", "9lives=cat": "9lives", "card=inner": "card", "hostname=": "hostname"} {
		for _, command := range []string{"print", "serve"} {
			alone(t)

			k := twoCards()
			_, err := run(t, kernel(k), command, "--listen", "127.0.0.1:0", "--labels", labels)
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "SAS_EXPORTER_LABELS") {
				t.Errorf("%s --labels %s = %v, want an error naming %q and the setting", command, labels, err, want)
			}
			if asked := k.Asked(polltest.HBAs); asked != 0 {
				t.Errorf("%s --labels %s asked the kernel %d questions before refusing", command, labels, asked)
			}
		}
	}
}

// print is also the check that the cards can be read, so a card that does not answer makes it fail, with the page still printed to show what that
// looks like, and the card that did answer still on it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintFailsWhenACardDoesNotAnswer(t *testing.T) {
	alone(t)

	k := twoCards()
	k.Fail(polltest.Of(polltest.Links, outside), errors.New("host reset in progress"))

	out, err := run(t, kernel(k), "print")
	if err == nil || !strings.Contains(err.Error(), outside) {
		t.Errorf("print with a card not answering = %v, want an error naming it", err)
	}
	if !strings.Contains(out, `sas_poll_failures_total{card="host8",poll="links"} 1`) || !strings.Contains(out, `sas_poll_failures_total{card="host1",poll="links"} 0`) || !strings.Contains(out, `sas_phy_link_state{card="host1",expander="",phy="0",state="up"} 1`) {
		t.Errorf("the page should show one card's poll failed and the other's phys:\n%s", out)
	}
}

// On a machine with no card there is nothing to print, and print says so with the setting to check rather than succeed with an empty page: run
// in a container that was not given the machine's /sys, it looks just like this.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintFailsOnAMachineWithNoCard(t *testing.T) {
	alone(t)

	out, err := run(t, connect, "print", "--sysfs", sysfstest.New(t).Root, "--dev", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no SAS card") || !strings.Contains(err.Error(), "SAS_EXPORTER_SYSFS") {
		t.Errorf("print with no card = %v, want an error saying so and naming the setting", err)
	}
	if count(out, "sas_card_up") != 0 || !strings.Contains(out, "sas_exporter_build_info") {
		t.Errorf("the page should hold the build and no card:\n%s", out)
	}
}

// A /sys that is not there stops both commands at once, naming the setting: it is a mistake in how the program was started, not a machine with no
// card.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestASysThatIsNotThere(t *testing.T) {
	for _, command := range []string{"print", "serve"} {
		alone(t)

		_, err := run(t, connect, command, "--listen", "127.0.0.1:0", "--sysfs", "/no/such/sys", "--dev", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "/no/such/sys") || !strings.Contains(err.Error(), "SAS_EXPORTER_SYSFS") {
			t.Errorf("%s --sysfs /no/such/sys = %v, want an error naming the path and the setting", command, err)
		}
	}
}

// A word for which hardware to ask that is none of them stops both commands before anything is asked, naming the word and the setting.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestASensorNobodyHasHeardOf(t *testing.T) {
	for _, command := range []string{"print", "serve"} {
		alone(t)

		k := twoCards()
		_, err := run(t, kernel(k), command, "--listen", "127.0.0.1:0", "--sensors", "fans")
		if err == nil || !strings.Contains(err.Error(), "fans") || !strings.Contains(err.Error(), "SAS_EXPORTER_SENSORS") {
			t.Errorf("%s --sensors fans = %v, want an error naming the word and the setting", command, err)
		}
		if asked := k.Asked(polltest.HBAs); asked != 0 {
			t.Errorf("%s --sensors fans asked the kernel %d questions before refusing", command, asked)
		}
	}
}

// freePort is an address on this machine nothing else is listening on: taken, noted and given back.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	return ln.Addr().String()
}

// fetch reads a path from the program once it is serving, trying for a second while it starts.
func fetch(t *testing.T, addr, path string) string {
	t.Helper()

	for range 50 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		return string(body)
	}
	t.Fatalf("nothing answered at %s%s", addr, path)

	return ""
}

// serve is the whole program: it polls the cards, serves what the kernel said, keeps one card's trouble from the other's readings, and stops
// cleanly when told to.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestServe(t *testing.T) {
	alone(t)

	k := twoCards()
	k.Fail(polltest.Of(polltest.Links, outside), errors.New("host reset in progress"))
	addr := freePort(t)

	root, _ := program(t, kernel(k), "serve", "--listen", addr)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- root.ExecuteContext(ctx) }()

	if got := strings.TrimSpace(fetch(t, addr, metrics.HealthPath)); got != "ok" {
		t.Errorf("%s = %q, want ok", metrics.HealthPath, got)
	}

	// the first round is asked as the program starts, so it is on the page within moments
	wanted := []string{`sas_card_up{card="host1"} 1`, `sas_phy_link_state{card="host1",expander="",phy="0",state="up"} 1`, `sas_poll_failures_total{card="host8",poll="links"} 1`, `sas_enclosure_slots{card="host8",enclosure="8:0:24:0"} 2`}
	var body string
	for range 50 {
		body = fetch(t, addr, metrics.Path)
		if !strings.Contains(body, wanted[len(wanted)-1]) || !strings.Contains(body, wanted[len(wanted)-2]) {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		break
	}
	for _, want := range wanted {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s:\n%s", want, body)
		}
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("serve stopped with %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop when told to")
	}
}

// A port that is already taken stops the program with the address in the error, and the polling it had started stops with it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestServeSaysWhenThePortIsTaken(t *testing.T) {
	alone(t)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()

	root, _ := program(t, kernel(twoCards()), "serve", "--listen", addr)
	stopped := make(chan error, 1)
	go func() { stopped <- root.ExecuteContext(t.Context()) }()

	select {
	case err := <-stopped:
		if err == nil || !strings.Contains(err.Error(), addr) {
			t.Errorf("serve on a taken port = %v, want an error naming %s", err, addr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not give up on a port that was taken")
	}
}
