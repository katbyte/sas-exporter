//go:build acceptance

package acceptance_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/metricstest"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
)

// image is the program's own image, as make docker builds it. make testacc builds it and names it here.
var image = os.Getenv("SAS_EXPORTER_TEST_PROGRAM_IMAGE")

// patience is how long a page is waited for before a test gives up on it.
const patience = 30 * time.Second

// fixture is where the saved /sys is put inside the container.
const fixture = "/fixture"

// docker runs one docker command and gives back what it printed. It is not stopped when the test ends, so a cleanup can still put things back.
func docker(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), "docker", args...) //nolint:gosec // every argument is one a test here wrote
	out, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(out)), err
}

// started starts the image under that name with these settings, asking the kernel every second, and gives back where its page is. A saved /sys
// given as root is copied in before it starts, and the program told to read it; with none it reads the container's own. It is removed when the
// test ends.
func started(t *testing.T, name, root string) string {
	t.Helper()

	if image == "" {
		t.Skip("no image to run: make testacc builds one and names it in SAS_EXPORTER_TEST_PROGRAM_IMAGE")
	}

	_, _ = docker(t, "rm", "--force", name)
	t.Cleanup(func() { _, _ = docker(t, "rm", "--force", name) })

	// the health check is asked every two seconds where the image says thirty, so the test need not wait half a minute for the first
	args := []string{"create", "--name", name, "--publish", "127.0.0.1::9088", "--health-interval", "2s", "--env", "SAS_EXPORTER_INTERVALS=links=1s,enclosures=1s,sensors=1s", "--env", "SAS_EXPORTER_LOG=INFO"}
	if root != "" {
		args = append(args, "--env", "SAS_EXPORTER_SYSFS="+fixture)
	}
	if out, err := docker(t, append(args, image)...); err != nil {
		t.Fatalf("making a container of the image %s: %v: %s", image, err, out)
	}

	if root != "" {
		if out, err := docker(t, "cp", root+"/.", name+":"+fixture); err != nil {
			t.Fatalf("handing the container the saved /sys: %v: %s", err, out)
		}
	}

	if out, err := docker(t, "start", name); err != nil {
		t.Fatalf("starting the container: %v: %s", err, out)
	}

	published, err := docker(t, "port", name, "9088/tcp")
	if err != nil || published == "" {
		t.Fatalf("finding where the image's port is published: %v: %s", err, published)
	}

	return "http://" + strings.TrimSpace(strings.Split(published, "\n")[0])
}

// count is how many readings of that name the page holds.
func count(t *testing.T, page, name string) int {
	t.Helper()

	return len(metricstest.Readings(t, page, name))
}

// served reads the page until it satisfies check or patience runs out, and gives back the last page read and whether it did.
func served(t *testing.T, base string, check func(page string) bool) (string, bool) {
	t.Helper()

	return metricstest.Until(t, base+metrics.Path, patience, check)
}

// healthy waits for the image's own health check to pass.
func healthy(t *testing.T, name string) {
	t.Helper()

	deadline := time.Now().Add(patience)
	for {
		health, err := docker(t, "inspect", "--format", "{{.State.Health.Status}}", name)
		if health == "healthy" {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("the image's own health check says %q (%v), want healthy", health, err)
			return
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// The image a deployment runs, handed the /sys of a real machine: it serves a page with both cards up and every expander, phy, disk and slot that
// machine has, passes its own health check, runs as someone other than root, and goes on asking. As that user it has no leave to ask the hardware
// itself, which costs it only what the hardware measures: that is said in its log once for each card, however many times it comes round. A card
// then taken away is reported as down while the other carries on, and what the lost card last said is left off the page once it is three polls old.
func TestTheImageReadsARealMachine(t *testing.T) {
	root, _ := sysfstest.NAS(t)
	name := "sas-exporter-acceptance-nas"
	base := started(t, name, root)

	whole := func(page string) bool {
		return count(t, page, "sas_card_up") == sysfstest.NASCards && count(t, page, "sas_phy_info") == sysfstest.NASPhys && count(t, page, "sas_enclosure_slot_status") == sysfstest.NASSlots
	}
	page, ok := served(t, base, whole)
	if !ok {
		logs, _ := docker(t, "logs", name)
		t.Fatalf("the image never served a page with both cards and all they have:\n%s\nits log:\n%s", page, logs)
	}

	for reading, want := range map[string]int{"sas_expander_info": sysfstest.NASExpanders, "sas_device_info": sysfstest.NASDisks, "sas_device_queue_depth": sysfstest.NASDisks, "sas_enclosure_info": sysfstest.NASEnclosures, "sas_enclosure_slot_powered": sysfstest.NASSlots, "sas_phy_errors_total": 4 * (sysfstest.NASPhys - sysfstest.NASRefused)} {
		if got := count(t, page, reading); got != want {
			t.Errorf("%d readings of %s, want %d", got, reading, want)
		}
	}
	for _, want := range []string{`sas_card_up{card="9500-16i"} 1`, `sas_card_up{card="9500-16e"} 1`, `sas_phy_errors_total{card="9500-16i",error="invalid_dword",expander="expander-1:0",phy="13"} 12`, `sas_poll_failures_total{card="9500-16i",poll="links"} 0`, `sas_phy_link_rate_gigabits_per_second{card="9500-16i",expander="expander-1:0",phy="12"} 12`, `sas_card_pcie_link_speed_gigatransfers_per_second{card="9500-16i"} 16`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s", want)
		}
	}

	if body := metricstest.Get(t, base+metrics.HealthPath); strings.TrimSpace(body) != "ok" {
		t.Errorf("%s answered %q, want ok", metrics.HealthPath, body)
	}
	if who, err := docker(t, "exec", name, "id", "-u"); err != nil || who == "" || who == "0" {
		t.Errorf("the program runs as user %q (%v), want someone other than root", who, err)
	}
	healthy(t, name)

	// it goes on asking on its own schedule, so a later scrape reads a later answer
	lastAsked := func(page string) float64 {
		at, _ := metricstest.One(t, page, "sas_poll_last_success_timestamp_seconds", map[string]string{"card": "9500-16i", "poll": "links"})
		return at
	}
	first := lastAsked(page)
	if later, asked := served(t, base, func(later string) bool { return lastAsked(later) > first }); !asked {
		t.Errorf("the card was last asked at %v and the page never showed a later answer than %v", lastAsked(later), first)
	}

	// by now the hardware has come round to be asked several times, and each time there was no leave to: none of what it measures is on the page,
	// that round is not said to have run or failed, and the log says why once for each card
	if count(t, page, "sas_card_temperature_celsius") != 0 || count(t, page, "sas_enclosure_element_status") != 0 || strings.Contains(page, `poll="sensors"`) {
		t.Errorf("the page has what only the hardware could have said, or says it was asked:\n%s", page)
	}
	logs, err := docker(t, "logs", name)
	if said := strings.Count(logs, "not everything it measures can be read from here"); err != nil || said != sysfstest.NASCards {
		t.Errorf("the log says %d times that the hardware could not be asked (%v), want once for each of %d cards:\n%s", said, err, sysfstest.NASCards, logs)
	}

	// a card falls off the bus: the kernel stops listing it
	if out, err := docker(t, "exec", "--user", "0", name, "rm", fixture+"/class/sas_host/host8"); err != nil {
		t.Fatalf("taking a card away: %v: %s", err, out)
	}

	down, ok := served(t, base, func(page string) bool {
		up, known := metricstest.One(t, page, "sas_card_up", map[string]string{"card": "9500-16e"})
		return known && up == 0 && count(t, page, `sas_phy_info`) < sysfstest.NASPhys
	})
	if !ok {
		t.Fatalf("a card went away and the page never showed it down with its phys left off:\n%s", down)
	}
	if up, _ := metricstest.One(t, down, "sas_card_up", map[string]string{"card": "9500-16i"}); up != 1 || !strings.Contains(down, `sas_phy_info{attached="upstream",card="9500-16i",expander="expander-1:0",phy="12"} 1`) {
		t.Errorf("the card that stayed should still be up with its phys on the page:\n%s", down)
	}
	if _, said := metricstest.One(t, down, "sas_card_info", map[string]string{"card": "9500-16e"}); said || strings.Contains(down, `sas_phy_info{attached="device",card="9500-16e"`) {
		t.Errorf("the card that went still has its phys or what it said of itself on the page:\n%s", down)
	}
	if failed, _ := metricstest.One(t, down, "sas_poll_failures_total", map[string]string{"card": "9500-16e", "poll": "links"}); failed < 1 {
		t.Errorf("the page counts %v failed polls of the card that went, want the ones that failed", failed)
	}
	// it is the card that is gone, not the program: its own health check still answers
	metricstest.Get(t, base+metrics.HealthPath)
}

// The image as it is deployed, given nothing: it reads the /sys docker gives every container. The machine docker runs on here has no SAS card, so
// the page holds the build that wrote it and no card, the program stays up and healthy with nothing to report, and print says there is no card
// rather than succeed with an empty page.
func TestTheImageOnAMachineWithNoCard(t *testing.T) {
	name := "sas-exporter-acceptance-bare"
	base := started(t, name, "")

	page, ok := served(t, base, func(page string) bool { return strings.Contains(page, "sas_exporter_build_info") })
	if !ok {
		logs, _ := docker(t, "logs", name)
		t.Fatalf("the image never served a page:\n%s\nits log:\n%s", page, logs)
	}
	if n := count(t, page, "sas_card_up"); n != 0 {
		t.Skipf("the machine docker runs on has %d SAS cards of its own, so this cannot be the test of a machine with none", n)
	}
	healthy(t, name)

	out, err := docker(t, "run", "--rm", image, "print")
	if err == nil || !strings.Contains(out, "no SAS card") || !strings.Contains(out, "SAS_EXPORTER_SYSFS") {
		t.Errorf("print with no card = %v, saying %q; want it to fail, say there is no card and name the setting", err, out)
	}
}
