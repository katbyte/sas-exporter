package sysfs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/pointer"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/sysfs"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
)

// card is the name the kernel gives the card most tests make.
const card = "host1"

// links is what hangs off the tree's card, and fails the test when it cannot be read at all.
func links(t *testing.T, tree *sysfstest.Tree) poll.Links {
	t.Helper()

	got, err := sysfs.New(tree.Root, sysfs.Devices{}).Links(t.Context(), card)
	if err != nil {
		t.Fatalf("links: %v", err)
	}

	return got
}

// phy is one phy of what was read, and fails the test when it is not there.
func phy(t *testing.T, l *poll.Links, expander string, number int) *poll.Phy {
	t.Helper()

	for i := range l.Phys {
		if l.Phys[i].Expander == expander && l.Phys[i].Number == number {
			return &l.Phys[i]
		}
	}
	t.Fatalf("no phy %d of %q among the %d read", number, expander, len(l.Phys))

	return nil
}

// device is the device with that serial number among what was read, and fails the test when it is not there.
func device(t *testing.T, l *poll.Links, serial string) *poll.Device {
	t.Helper()

	for i := range l.Devices {
		if l.Devices[i].Serial == serial {
			return &l.Devices[i]
		}
	}
	t.Fatalf("no device with the serial number %q among the %d read", serial, len(l.Devices))

	return nil
}

// A machine whose kernel has no driver for a SAS card loaded has nowhere to list one. That is no cards, and not something going wrong.
func TestAMachineWithNoCard(t *testing.T) {
	t.Parallel()

	hbas, err := sysfs.New(sysfstest.New(t).Root, sysfs.Devices{}).HBAs(t.Context())
	if err != nil || len(hbas) != 0 {
		t.Errorf("cards = %+v (%v), want none and no error", hbas, err)
	}
}

// What a card says of itself is what tells one card from another on a dashboard, and which firmware it runs. The values are padded and prefixed
// by the kernel, and come off the page without either.
func TestWhatACardSaysOfItself(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	first, second := tree.Card(1), tree.Card(8)
	tree.Write(second.Attrs(), "board_name", "HBA 9500-16e")
	tree.Write(second.Attrs(), "ioc_reset_count", "3")
	tree.Write(second.Attrs(), "state", "recovery")

	hbas, err := sysfs.New(tree.Root, sysfs.Devices{}).HBAs(t.Context())
	if err != nil || len(hbas) != 2 {
		t.Fatalf("cards = %+v (%v), want two", hbas, err)
	}

	want := poll.HBA{Name: card, SASAddress: "5000000000000001", Model: "HBA 9500-16i", Firmware: "32.00.00.00", BIOS: "09.27.00.00", Driver: "mpt3sas", DriverVersion: "48.100.00.00", Running: true}
	got := hbas[0]
	if got.Resets == nil || pointer.From(got.Resets) != 0 {
		t.Errorf("resets = %v, want a count of 0", got.Resets)
	}
	if got.Slot == nil || pointer.From(got.Slot) != (poll.PCIeLink{Speed: 16, MaximumSpeed: 16, Width: 8, MaximumWidth: 8}) {
		t.Errorf("the card's own link to the machine = %+v, want eight lanes at 16 gigatransfers a second", got.Slot)
	}
	got.Resets, got.Slot = nil, nil
	if got != want {
		t.Errorf("the first card = %+v, want %+v", got, want)
	}

	if other := hbas[1]; other.Name != "host8" || other.Model != "HBA 9500-16e" || other.Running || pointer.From(other.Resets) != 3 {
		t.Errorf("the second card = %+v, want an HBA 9500-16e being reset for the fourth time", other)
	}

	_ = first
}

// Only whether a card is in service is something every driver says. A card whose driver gives no model, no versions and no count of resets is
// still reported, without them, and its address is taken from its first phy, which carries the card's own.
func TestACardWhoseDriverSaysLittle(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	for _, file := range []string{"board_name", "version_fw", "version_bios", "proc_name", "ioc_reset_count", "host_sas_address"} {
		tree.Remove(node.Attrs(), file)
	}
	tree.Remove(node.Slot(), "current_link_width")

	hbas, err := sysfs.New(tree.Root, sysfs.Devices{}).HBAs(t.Context())
	if err != nil || len(hbas) != 1 {
		t.Fatalf("cards = %+v (%v), want one", hbas, err)
	}
	if want := (poll.HBA{Name: card, SASAddress: "5000000000000001", Running: true}); hbas[0] != want {
		t.Errorf("the card = %+v, want %+v", hbas[0], want)
	}

	// a card that does not say whether it is in service cannot be called up or down
	tree.Remove(node.Attrs(), "state")
	if _, err := sysfs.New(tree.Root, sysfs.Devices{}).HBAs(t.Context()); err == nil || !strings.Contains(err.Error(), card) {
		t.Errorf("a card with no state = %v, want an error naming it", err)
	}
}

// What is on the other end of a phy is what tells a bad cable from a bad slot: the lanes between a card and a backplane, the ones on to a
// further backplane, the ones to disks, and the ones with nothing on them.
func TestWhatIsOnTheOtherEndOfEachPhy(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 3, sysfstest.Rate12)
	node.Phy(4, sysfstest.Rate6)
	node.Phy(5, sysfstest.NoLink)
	node.Disk(4, "sda", "DIRECT01")

	backplane := node.Expander(0, 1, 2, 3)
	backplane.Phy(0, sysfstest.Rate12)
	backplane.Phy(1, sysfstest.NoLink)
	backplane.Phys(4, 7, sysfstest.Rate12)
	backplane.Phys(8, 9, sysfstest.Rate12)
	backplane.Phys(10, 11, sysfstest.Rate12)
	backplane.Disk(0, "sdb", "SLOT0001")
	backplane.Enclosure(10)

	further := backplane.Expander(8, 9)
	further.Phys(0, 1, sysfstest.Rate12)
	further.Phy(2, sysfstest.Failed)

	got := links(t, tree)
	if len(got.Phys) != 6+10+3 || got.Unread != nil {
		t.Fatalf("read %d phys with %v unread, want 19 and nothing unread", len(got.Phys), got.Unread)
	}

	for _, want := range []struct {
		expander string
		number   int
		link     poll.LinkState
		attached poll.Attached
	}{
		{"", 0, poll.LinkUp, poll.AttachedExpander},
		{"", 3, poll.LinkUp, poll.AttachedExpander},
		{"", 4, poll.LinkUp, poll.AttachedDevice},
		{"", 5, poll.LinkDown, poll.AttachedNone},
		{backplane.Name, 0, poll.LinkUp, poll.AttachedDevice},
		{backplane.Name, 1, poll.LinkDown, poll.AttachedNone},
		// the four lanes back to the card: the kernel lists nothing below them, and the card's port to here is four wide
		{backplane.Name, 4, poll.LinkUp, poll.AttachedUpstream},
		{backplane.Name, 7, poll.LinkUp, poll.AttachedUpstream},
		{backplane.Name, 8, poll.LinkUp, poll.AttachedExpander},
		// an enclosure's own processor is at the end of a line like a disk
		{backplane.Name, 10, poll.LinkUp, poll.AttachedDevice},
		// and a phy for the expander's own inner workings has a link and nothing on it
		{backplane.Name, 11, poll.LinkUp, poll.AttachedNone},
		{further.Name, 0, poll.LinkUp, poll.AttachedUpstream},
		{further.Name, 1, poll.LinkUp, poll.AttachedUpstream},
		{further.Name, 2, poll.LinkFailed, poll.AttachedNone},
	} {
		if p := phy(t, &got, want.expander, want.number); p.Link != want.link || p.Attached != want.attached {
			t.Errorf("phy %d of %q is %s with %s on the other end, want %s with %s", want.number, want.expander, p.Link, p.Attached, want.link, want.attached)
		}
	}

	if p := phy(t, &got, "", 4); p.Rate != 6 || p.Maximum != 12 {
		t.Errorf("the phy to the disk runs at %v of %v gigabits a second, want 6 of 12", p.Rate, p.Maximum)
	}
	if p := phy(t, &got, "", 5); p.Rate != 0 {
		t.Errorf("a phy with no link runs at %v, want 0", p.Rate)
	}

	if len(got.Expanders) != 2 || got.Expanders[0].Name != backplane.Name || got.Expanders[1].Name != further.Name {
		t.Errorf("expanders = %+v, want the backplane and the one behind it", got.Expanders)
	}
}

// A wide link that has lost a lane is the fault a count of lanes is there to show, so the lanes back to the card are counted off against how
// wide the port leading here is now: seven, when the card's port has dropped to seven.
func TestTheLanesBackToTheCardFollowThePort(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 7, sysfstest.Rate12)
	node.Phy(7, sysfstest.NoLink)

	backplane := node.Expander(0, 1, 2, 3, 4, 5, 6)
	backplane.Phys(12, 18, sysfstest.Rate12)
	backplane.Phy(19, sysfstest.NoLink)
	backplane.Phy(42, sysfstest.Rate12)

	got := links(t, tree)

	var upstream int
	for i := range got.Phys {
		if got.Phys[i].Attached == poll.AttachedUpstream {
			upstream++
		}
	}
	if upstream != 7 {
		t.Errorf("%d lanes lead back to the card, want the 7 that still have a link", upstream)
	}
	if p := phy(t, &got, backplane.Name, 19); p.Link != poll.LinkDown || p.Attached != poll.AttachedNone {
		t.Errorf("the lane that dropped is %s with %s on it, want down with none", p.Link, p.Attached)
	}
	if p := phy(t, &got, backplane.Name, 42); p.Attached != poll.AttachedNone {
		t.Errorf("the expander's own phy has %s on it, want none: it is numbered after the lanes", p.Attached)
	}
}

// The error counts are the point of the whole thing, read as the kernel gives them: four to a phy.
func TestAPhysErrorCounts(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12, 13, 2, 3, 1)
	node.Phy(1, sysfstest.Rate12)

	got := links(t, tree)
	if e := phy(t, &got, "", 0).Errors; e == nil || pointer.From(e) != (poll.PhyErrors{InvalidDwords: 13, RunningDisparity: 2, LossOfDwordSync: 3, ResetProblems: 1}) {
		t.Errorf("error counts = %+v, want 13, 2, 3 and 1", e)
	}
	if e := phy(t, &got, "", 1).Errors; e == nil || pointer.From(e) != (poll.PhyErrors{}) {
		t.Errorf("error counts = %+v, want four of 0: a phy that has counted nothing still counts", e)
	}
}

// One phy whose counts cannot be read must not cost the others theirs: it is left without, what went wrong is said, and the rest are as read. A
// phy with no counts to read at all is simply one that keeps none.
func TestAPhyWhoseCountsCannotBeRead(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12, 5)
	broken := node.Phy(1, sysfstest.Rate12, 6)
	without := node.Phy(2, sysfstest.Rate12)
	node.Phy(3, sysfstest.Rate12, 7)

	tree.Break(broken, "running_disparity_error_count")
	for _, file := range []string{"invalid_dword_count", "running_disparity_error_count", "loss_of_dword_sync_count", "phy_reset_problem_count"} {
		tree.Remove(without, file)
	}
	tree.Write(node.Phy(4, sysfstest.Rate12), "invalid_dword_count", "lots")

	got := links(t, tree)
	for number, want := range map[int]uint64{0: 5, 3: 7} {
		if e := phy(t, &got, "", number).Errors; e == nil || e.InvalidDwords != want {
			t.Errorf("phy %d: error counts = %+v, want %d garbled words", number, e, want)
		}
	}
	for _, number := range []int{1, 2, 4} {
		if e := phy(t, &got, "", number).Errors; e != nil {
			t.Errorf("phy %d: error counts = %+v, want none", number, e)
		}
	}

	if got.Unread == nil || !strings.Contains(got.Unread.Error(), "phy-1:1") || !strings.Contains(got.Unread.Error(), "phy-1:4") || strings.Contains(got.Unread.Error(), "phy-1:2") {
		t.Errorf("unread = %v, want the phy that could not be read and the one that made no sense, and not the one that keeps no counts", got.Unread)
	}
}

// A machine with a lot wrong must not write a book in the log: the first few things that could not be read are named and the rest counted.
func TestManyThingsUnreadAreCounted(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	for number := range 10 {
		tree.Break(node.Phy(number, sysfstest.Rate12), "invalid_dword_count")
	}

	got := links(t, tree)
	if got.Unread == nil || strings.Count(got.Unread.Error(), "\n") != 3 || !strings.Contains(got.Unread.Error(), "and 7 more") {
		t.Errorf("unread = %v, want three named and seven counted", got.Unread)
	}
}

// A disk is reported under what joins it to what other tools say of the same disk, its serial number, with where it is: the phy its link is on, and
// the enclosure and slot it sits in.
func TestADisk(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 3, sysfstest.Rate12)
	backplane := node.Expander(0, 1, 2, 3)
	backplane.Phy(5, sysfstest.Rate12)
	backplane.Phy(40, sysfstest.Rate12)

	disk := backplane.Disk(5, "sdab", "Z0000030")
	tree.Write(disk, "iorequest_cnt", "0x1335430")
	tree.Write(disk, "iodone_cnt", "0x133542f")
	tree.Write(disk, "ioerr_cnt", "0x1f")
	tree.Write(disk, "iotmo_cnt", "0x2")
	shelf := backplane.Enclosure(40)
	shelf.Slot(17, "OK", disk)

	got := links(t, tree)
	if len(got.Devices) != 1 {
		t.Fatalf("devices = %+v, want the one disk and not the enclosure's own processor", got.Devices)
	}

	d := got.Devices[0]
	if d.Errors == nil || d.Timeouts == nil || pointer.From(d.Errors) != 31 || pointer.From(d.Timeouts) != 2 {
		t.Errorf("command errors = %v and timeouts = %v, want 31 and 2", d.Errors, d.Timeouts)
	}
	// the kernel writes these counts in sixteens: one command of some twenty million is still out with the disk
	if d.Sent == nil || d.Completed == nil || pointer.From(d.Sent) != 20141104 || pointer.From(d.Sent)-pointer.From(d.Completed) != 1 {
		t.Errorf("commands sent = %v and completed = %v, want 20141104 and one fewer", d.Sent, d.Completed)
	}
	d.Sent, d.Completed, d.Errors, d.Timeouts = nil, nil, nil, nil

	want := poll.Device{SASAddress: "5000000000000003", Serial: "Z0000030", Disk: "sdab", WWN: "5000000000000004", Vendor: "WDC", Model: "WUH722020BL5204", Revision: "C54K", Expander: backplane.Name, Phy: 5, Enclosure: shelf.Name, Slot: "17", State: "running"}
	if d != want {
		t.Errorf("the disk = %+v\nwant       %+v", d, want)
	}
}

// What a disk does not say is left out rather than made up: one with no serial number, no name yet, no world wide name, no slot and a kernel too old
// to count its timeouts is still reported, by its address.
func TestADiskTheKernelSaysLittleOf(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	node.Phy(1, sysfstest.Rate12)

	bare := node.Disk(0, "sda", "GONE")
	for _, file := range []string{"vpd_pg80", "wwid", "iorequest_cnt", "iodone_cnt", "iotmo_cnt", "ioerr_cnt", "vendor", "model", "rev", "block"} {
		tree.Remove(bare, file)
	}
	tree.Write(bare, "state", "transport-offline")

	tree.Write(node.Disk(1, "sdb", "ODD00001"), "wwid", "t10.ATA     Samsung SSD 870 EVO 4TB")

	got := links(t, tree)
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v, want both", got.Devices)
	}

	if d, want := pointer.From(device(t, &got, "")), (poll.Device{SASAddress: "5000000000000002", Phy: 0, State: "transport_offline"}); d != want {
		t.Errorf("the bare disk = %+v, want %+v", d, want)
	}
	if d := device(t, &got, "ODD00001"); d.WWN != "" || d.Enclosure != "" || d.Slot != "" {
		t.Errorf("the disk with no world wide name and no slot = %+v, want neither", d)
	}
}

// A device the kernel has not looked at yet has nothing to report, and is picked up at a later round once it has.
func TestADeviceTheKernelHasNotLookedAt(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	disk := node.Disk(0, "sda", "LATE0001")
	tree.Remove(filepath.Dir(disk))

	got := links(t, tree)
	if len(got.Devices) != 0 || got.Unread != nil {
		t.Errorf("devices = %+v with %v unread, want none and nothing unread", got.Devices, got.Unread)
	}
	if p := phy(t, &got, "", 0); p.Attached != poll.AttachedDevice {
		t.Errorf("the phy has %s on it, want device: the kernel lists the end of the line", p.Attached)
	}
}

// What an enclosure says of its slots, with the slots in the order of their numbers, the kernel's words as the page writes them, and a light that
// is on whichever of the ways it can be on.
func TestAnEnclosureAndItsSlots(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 3, sysfstest.Rate12)
	backplane := node.Expander(0, 1, 2, 3)
	backplane.Phy(40, sysfstest.Rate12)

	shelf := backplane.Enclosure(40)
	for number := range 12 {
		shelf.Slot(number, "OK", "")
	}
	tree.Write(shelf.Slot(3, "not installed", ""), "locate", "1")
	tree.Write(shelf.Slot(7, "critical", ""), "fault", "2")
	tree.Write(shelf.Slot(8, "non-critical", ""), "fault", "4")

	got, err := sysfs.New(tree.Root, sysfs.Devices{}).Enclosures(t.Context(), card)
	if err != nil || len(got) != 1 {
		t.Fatalf("enclosures = %+v (%v), want one", got, err)
	}

	e := got[0]
	if e.Name != shelf.Name || e.ID != "5000000000000004" || e.Vendor != "SMC" || e.Product != "SC846-P" || e.Revision != "100b" || e.Expander != backplane.Name {
		t.Errorf("the enclosure = %+v, want a Supermicro SC846-P on the backplane's expander", e)
	}

	order := make([]string, 0, len(e.Slots))
	for _, slot := range e.Slots {
		order = append(order, slot.Number)
	}
	if strings.Join(order, " ") != "0 1 2 3 4 5 6 7 8 9 10 11" {
		t.Errorf("slots in the order %v, want 0 to 11 by number", order)
	}

	for number, want := range map[int]poll.Slot{
		0: {Number: "0", Status: "ok"},
		3: {Number: "3", Status: "not_installed", Locate: true},
		7: {Number: "7", Status: "critical", Fault: true},
		8: {Number: "8", Status: "non_critical", Fault: true},
	} {
		if e.Slots[number] != want {
			t.Errorf("slot %d = %+v, want %+v", number, e.Slots[number], want)
		}
	}
}

// An enclosure that gives a slot no number has it go by the name the enclosure calls it, on the slot and on the disk in it alike, so the two still
// meet.
func TestASlotWithNoNumber(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phys(0, 1, sysfstest.Rate12)
	disk := node.Disk(0, "sda", "Z0000001")
	shelf := node.Enclosure(1)
	tree.Write(shelf.Slot(0, "OK", disk), "slot", "-1")

	enclosures, err := sysfs.New(tree.Root, sysfs.Devices{}).Enclosures(t.Context(), card)
	if err != nil || len(enclosures) != 1 || len(enclosures[0].Slots) != 1 {
		t.Fatalf("enclosures = %+v (%v), want one of one slot", enclosures, err)
	}

	got := links(t, tree)
	if slot, seat := enclosures[0].Slots[0].Number, device(t, &got, "Z0000001").Slot; slot != "Slot00" || seat != "Slot00" {
		t.Errorf("the slot goes by %q and the disk in it by %q, want Slot00 for both", slot, seat)
	}
	if enclosures[0].Expander != "" {
		t.Errorf("an enclosure on the card itself speaks through %q, want no expander", enclosures[0].Expander)
	}
}

// A slot that cannot be read fails the question whole, so the last good answer is kept rather than a shelf shown with a slot missing.
func TestASlotThatCannotBeRead(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	shelf := node.Enclosure(0)
	shelf.Slot(0, "OK", "")
	tree.Break(shelf.Slot(1, "OK", ""), "status")

	if got, err := sysfs.New(tree.Root, sysfs.Devices{}).Enclosures(t.Context(), card); err == nil || !strings.Contains(err.Error(), shelf.Name) {
		t.Errorf("enclosures = %+v (%v), want an error naming the enclosure", got, err)
	}
}

// A card the kernel no longer has cannot be asked about, and a question cut short because the program is stopping is given up.
func TestACardThatIsNotThereAndAQuestionCutShort(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12)
	node.Enclosure(0).Slot(0, "OK", "")
	k := sysfs.New(tree.Root, sysfs.Devices{})

	stopped, stop := context.WithCancel(t.Context())
	stop()

	if _, err := k.HBAs(stopped); err == nil {
		t.Error("the cards were listed for a program that is stopping")
	}
	if _, err := k.Links(stopped, card); err == nil {
		t.Error("links were read for a program that is stopping")
	}
	if _, err := k.Enclosures(stopped, card); err == nil {
		t.Error("enclosures were read for a program that is stopping")
	}

	if err := os.Remove(node.Class()); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Links(t.Context(), card); err == nil {
		t.Error("links were read of a card that is gone")
	}
	if _, err := k.Enclosures(t.Context(), card); err == nil {
		t.Error("enclosures were read of a card that is gone")
	}
}
