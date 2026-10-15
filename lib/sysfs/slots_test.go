package sysfs

import (
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
)

// An enclosure whose driver cannot say whether a slot has power is refused the moment it is asked. That costs its slots their power and nothing
// else: the rest of what the enclosure says of them is still read.
func TestAnEnclosureThatCannotSayWhetherASlotHasPower(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	tree.Write(node.Enclosure(0).Slot(0, "OK", ""), "power_status", "on")

	k := New(tree.Root, Devices{})
	k.read = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "power_status") {
			return nil, &os.PathError{Op: "read", Path: path, Err: syscall.ENOTTY}
		}

		return os.ReadFile(path) //nolint:gosec // G304: the path is one the reader asked for, under the test's own directory
	}

	got, err := k.Enclosures(t.Context(), "host1")
	if err != nil || len(got) != 1 || len(got[0].Slots) != 1 {
		t.Fatalf("enclosures = %+v (%v), want one of one slot", got, err)
	}
	if slot := got[0].Slots[0]; slot.Powered != nil || slot.Status != "ok" {
		t.Errorf("the slot = %+v, want it ok with nothing said of its power", slot)
	}
}
