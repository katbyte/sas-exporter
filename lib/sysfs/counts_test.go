package sysfs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
)

// refusing is a kernel that reads the tree at root but will not read the files check picks out: each fails after taking that long, the way the
// real one fails when the card or the expander will not give a phy's error counts. It also notes every file it was asked for.
func refusing(root string, took time.Duration, check func(path string) bool) (k *Kernel, asked *[]string) {
	asked = &[]string{}
	k = New(root, Devices{})
	k.read = func(path string) ([]byte, error) {
		*asked = append(*asked, path)
		if check(path) {
			time.Sleep(took)

			return nil, &os.PathError{Op: "read", Path: path, Err: syscall.EINVAL}
		}

		return os.ReadFile(path) //nolint:gosec // G304: the path is one the reader asked for, under the test's own directory
	}

	return k, asked
}

// phyOf is one phy among those read.
func phyOf(t *testing.T, l *poll.Links, expander string, number int) *poll.Phy {
	t.Helper()

	for i := range l.Phys {
		if l.Phys[i].Expander == expander && l.Phys[i].Number == number {
			return &l.Phys[i]
		}
	}
	t.Fatalf("no phy %d of %q among the %d read", number, expander, len(l.Phys))

	return nil
}

// An expander has phys with nothing wired to them, and refuses at once when asked for their error counts. That is a phy that keeps none, every
// round, and not a round that failed: the phy is left without, nothing is said to be unread, and it is asked for one count rather than all four.
func TestAPhyThatKeepsNoCounts(t *testing.T) {
	t.Parallel()

	tree := sysfstest.New(t)
	node := tree.Card(1)
	node.Phy(0, sysfstest.Rate12, 5)
	vacant := node.Phy(1, sysfstest.NoLink)
	node.Phy(2, sysfstest.Rate12, 7)

	k, asked := refusing(tree.Root, 0, func(path string) bool { return strings.HasPrefix(path, vacant) && strings.HasSuffix(path, "_count") })
	got, err := k.Links(t.Context(), "host1")
	if err != nil || got.Unread != nil {
		t.Fatalf("links: %v, with %v unread; want neither", err, got.Unread)
	}

	if e := phyOf(t, &got, "", 1).Errors; e != nil {
		t.Errorf("the vacant phy's error counts = %+v, want none", e)
	}
	if a, b := phyOf(t, &got, "", 0).Errors, phyOf(t, &got, "", 2).Errors; a == nil || b == nil || a.InvalidDwords != 5 || b.InvalidDwords != 7 {
		t.Errorf("the other phys' error counts = %+v and %+v, want 5 and 7 garbled words", a, b)
	}

	var counts int
	for _, path := range *asked {
		if strings.HasPrefix(path, vacant) && strings.HasSuffix(path, "_count") {
			counts++
		}
	}
	if counts != 1 {
		t.Errorf("the vacant phy was asked for %d of its counts, want only the first: the rest would be refused the same way", counts)
	}
}

// A card that has stopped answering takes the kernel ten seconds to give up on for every count asked of it, and the kernel may reset the card each
// time. So the first count that takes that long is the last one asked for that round: the rest of the card's phys are read without their counts,
// and the round is said to have failed.
func TestACardThatStopsAnsweringIsAskedNoMore(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		tree := sysfstest.New(t)
		node := tree.Card(1)
		node.Phys(0, 3, sysfstest.Rate12)
		backplane := node.Expander(0, 1, 2, 3)
		backplane.Phys(0, 9, sysfstest.Rate12)

		hung := filepath.Join(backplane.Dir, "phy-1:0:2")
		k, asked := refusing(tree.Root, 10*time.Second, func(path string) bool { return strings.HasPrefix(path, hung) && strings.HasSuffix(path, "_count") })

		start := time.Now()
		got, err := k.Links(t.Context(), "host1")
		if err != nil {
			t.Fatalf("links: %v", err)
		}

		if ended := time.Now(); !ended.Equal(start.Add(10 * time.Second)) {
			t.Errorf("the round took %s, want the 10s of the one count that hung and no more", ended.Sub(start))
		}
		if got.Unread == nil || !strings.Contains(got.Unread.Error(), "phy-1:0:2") || !strings.Contains(got.Unread.Error(), "no more error counts") {
			t.Errorf("unread = %v, want the phy that hung and that no more were asked for", got.Unread)
		}

		// the card's own phys and the two before the one that hung were read first; every phy after it is still listed, without counts
		if len(got.Phys) != 14 {
			t.Fatalf("read %d phys, want all 14", len(got.Phys))
		}
		for number := range 10 {
			if e := phyOf(t, &got, backplane.Name, number).Errors; (e != nil) != (number < 2) {
				t.Errorf("phy %d of the backplane: error counts = %+v, want them only for the phys read before the one that hung", number, e)
			}
		}

		after := slices.IndexFunc(*asked, func(path string) bool { return strings.HasPrefix(path, hung) && strings.HasSuffix(path, "_count") })
		for _, path := range (*asked)[after+1:] {
			if strings.HasSuffix(path, "_count") {
				t.Errorf("%s was asked for after the card stopped answering", path)
			}
		}
	})
}

// The real machine, read as its kernel gave it, refusals and all: two cards with a backplane behind each and a second chained behind that, every
// phy, the lanes between them told apart from the slots, and every disk with its serial number and the slot it sits in.
func TestTheRealMachine(t *testing.T) {
	t.Parallel()

	root, capture := sysfstest.NAS(t)
	if len(capture.Refused) != 4*sysfstest.NASRefused {
		t.Fatalf("the capture has %d files the kernel refused, want four for each of %d phys", len(capture.Refused), sysfstest.NASRefused)
	}

	k, _ := refusing(root, 0, func(path string) bool { return slices.Contains(capture.Refused, path) })

	hbas, err := k.HBAs(t.Context())
	if err != nil || len(hbas) != sysfstest.NASCards {
		t.Fatalf("cards = %+v (%v), want %d", hbas, err, sysfstest.NASCards)
	}
	if hbas[0].Name != "host1" || hbas[0].Model != "HBA 9500-16i" || hbas[1].Name != "host8" || hbas[1].Model != "HBA 9500-16e" || !hbas[0].Running || hbas[0].DriverVersion != "48.100.00.00" {
		t.Errorf("cards = %+v, want an HBA 9500-16i as host1 and an HBA 9500-16e as host8, both in service", hbas)
	}
	for i, hba := range hbas {
		if hba.Slot == nil || *hba.Slot != (poll.PCIeLink{Speed: 16, MaximumSpeed: 16, Width: 8, MaximumWidth: 8}) {
			t.Errorf("%s: its own link to the machine is %+v, want eight lanes at 16 gigatransfers a second, the most it can do", hba.Name, hba.Slot)
		}

		// the serial numbers are made up, in the order the cards were captured
		if hba.Serial != fmt.Sprintf("C%09d", i+1) || hba.Chip != "SAS3816" || hba.Assembly == "" || !strings.HasPrefix(hba.NVData, "32.01.00.") || hba.NVDataDefault != hba.NVData || hba.MPI != "206.3d" {
			t.Errorf("%s: says of itself %+v, want a made-up serial number, a SAS3816 chip, and the settings it came with", hba.Name, hba)
		}
		if hba.InFlight == nil || hba.Queue == nil || *hba.Queue != 4336 {
			t.Errorf("%s: %v commands in flight of at most %v, want a count of at most 4336", hba.Name, hba.InFlight, hba.Queue)
		}
	}

	var expanders, phys, disks, enclosures, slots, without, speedless int
	attached, serials, seated := map[poll.Attached]int{}, map[string]bool{}, 0
	for _, hba := range hbas {
		links, err := k.Links(t.Context(), hba.Name)
		if err != nil || links.Unread != nil {
			t.Fatalf("%s: links: %v, with %v unread; want neither", hba.Name, err, links.Unread)
		}

		expanders, phys, disks = expanders+len(links.Expanders), phys+len(links.Phys), disks+len(links.Devices)
		for i := range links.Phys {
			p := &links.Phys[i]
			attached[p.Attached]++

			// every link on this machine that came up did so at 12 gigabits a second
			if (p.Link == poll.LinkUp) != (p.Rate == 12) {
				t.Errorf("%s phy %d of %q is %s at %v gigabits a second", hba.Name, p.Number, p.Expander, p.Link, p.Rate)
			}

			// a phy that says what speeds it can do says all four, and can do 12 and is set to
			if p.Maximum == 0 && p.Minimum == 0 && p.HardwareMaximum == 0 && p.HardwareMinimum == 0 {
				speedless++
			} else if p.Maximum != 12 || p.HardwareMaximum != 12 || p.Minimum == 0 || p.HardwareMinimum == 0 {
				t.Errorf("%s phy %d of %q is set to between %v and %v gigabits a second and can do between %v and %v", hba.Name, p.Number, p.Expander, p.Minimum, p.Maximum, p.HardwareMinimum, p.HardwareMaximum)
			}

			if p.Errors == nil {
				without++
				continue
			}
			// the only errors this machine has counted are on the lanes between each card and its first backplane, at the backplane's end
			if (*p.Errors != poll.PhyErrors{}) != (p.Attached == poll.AttachedUpstream && strings.HasSuffix(p.Expander, ":0")) {
				t.Errorf("%s phy %d of %q, with %s on it, has counted %+v", hba.Name, p.Number, p.Expander, p.Attached, *p.Errors)
			}
		}

		for i := range links.Devices {
			d := &links.Devices[i]
			serials[d.Serial] = true
			if d.Enclosure != "" && d.Slot != "" {
				seated++
			}
			if d.Serial == "" || d.Disk == "" || d.WWN == "" || d.State != "running" || d.Expander == "" || d.Phy < 0 || d.Errors == nil || d.Timeouts == nil || d.Sent == nil || d.Completed == nil || *d.Sent == 0 {
				t.Errorf("%s: a disk with something missing: %+v", hba.Name, *d)
			}
			if d.QueueDepth == nil || *d.QueueDepth != 64 || d.Timeout == nil || *d.Timeout != 30 {
				t.Errorf("%s: disk %s takes %v commands at once and is given %v seconds for each, want 64 and 30", hba.Name, d.Disk, d.QueueDepth, d.Timeout)
			}
		}

		for _, e := range links.Expanders {
			if e.ChipVendor != "LSI" || !slices.Contains([]string{"562", "563", "565"}, e.Chip) || e.ChipRevision != "3" {
				t.Errorf("%s: expander %s is built on %q %q revision %q, want one of LSI's", hba.Name, e.Name, e.ChipVendor, e.Chip, e.ChipRevision)
			}
		}

		shelves, err := k.Enclosures(t.Context(), hba.Name)
		if err != nil {
			t.Fatalf("%s: enclosures: %v", hba.Name, err)
		}
		enclosures += len(shelves)
		for _, shelf := range shelves {
			slots += len(shelf.Slots)
			// a Supermicro backplane puts its firmware version where its maker may say what it likes
			if !strings.HasPrefix(shelf.VendorSpecific, "x") || !strings.Contains(shelf.VendorSpecific, "-66.16.1") {
				t.Errorf("%s: enclosure %s says %q of itself, want its firmware version", hba.Name, shelf.Name, shelf.VendorSpecific)
			}
			if shelf.Vendor != "SMC" || shelf.ID == "" || shelf.Expander == "" {
				t.Errorf("%s: an enclosure with something missing: %+v", hba.Name, shelf)
			}
			for _, slot := range shelf.Slots {
				if slot.Powered == nil || !*slot.Powered {
					t.Errorf("%s: slot %s of %s has power %v, want every slot on", hba.Name, slot.Number, shelf.Name, slot.Powered)
				}
			}
		}
	}

	if expanders != sysfstest.NASExpanders || phys != sysfstest.NASPhys || disks != sysfstest.NASDisks || enclosures != sysfstest.NASEnclosures || slots != sysfstest.NASSlots {
		t.Errorf("read %d expanders, %d phys, %d disks, %d enclosures and %d slots; want %d, %d, %d, %d and %d", expanders, phys, disks, enclosures, slots, sysfstest.NASExpanders, sysfstest.NASPhys, sysfstest.NASDisks, sysfstest.NASEnclosures, sysfstest.NASSlots)
	}
	if without != sysfstest.NASRefused {
		t.Errorf("%d phys have no error counts, want the %d the expanders refused", without, sysfstest.NASRefused)
	}
	if speedless != sysfstest.NASSpeedless {
		t.Errorf("%d phys say nothing of the speeds they can do, want %d", speedless, sysfstest.NASSpeedless)
	}
	if len(serials) != sysfstest.NASDisks || seated != sysfstest.NASDisks {
		t.Errorf("%d serial numbers between %d disks, %d of them in a slot; want every disk with its own and in one", len(serials), disks, seated)
	}

	// eight lanes from each card to its first backplane and eight on to the second, seen from both ends; a disk or an enclosure's processor on 62
	want := map[poll.Attached]int{poll.AttachedExpander: 32, poll.AttachedUpstream: 32, poll.AttachedDevice: sysfstest.NASDisks + sysfstest.NASEnclosures, poll.AttachedNone: sysfstest.NASPhys - 32 - 32 - sysfstest.NASDisks - sysfstest.NASEnclosures}
	for kind, n := range want {
		if attached[kind] != n {
			t.Errorf("%d phys have %s on the other end, want %d", attached[kind], kind, n)
		}
	}
}
