package sysfs

import (
	"slices"
	"testing"

	"github.com/katbyte/sas-exporter/lib/poll"
)

// The kernel says a link's speed in words, and the same words say when there is none. Each is a state on the page and a speed in gigabits a
// second, as SAS speeds are always given, 0 with no link; a word never seen before is other rather than a link that is down, so a kernel that
// learns a new one shows.
func TestWhatTheKernelSaysOfALink(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]struct {
		state poll.LinkState
		rate  float64
	}{
		"1.5 Gbit":                {poll.LinkUp, 1.5},
		"3.0 Gbit":                {poll.LinkUp, 3},
		"6.0 Gbit":                {poll.LinkUp, 6},
		"12.0 Gbit":               {poll.LinkUp, 12},
		"22.5 Gbit":               {poll.LinkUp, 22.5},
		"Unknown":                 {poll.LinkDown, 0},
		"Phy disabled":            {poll.LinkDisabled, 0},
		"Link Rate failed":        {poll.LinkFailed, 0},
		"Spinup hold":             {poll.LinkSpinupHold, 0},
		"":                        {poll.LinkOther, 0},
		"Phy reset in progress":   {poll.LinkOther, 0},
		"fast Gbit":               {poll.LinkOther, 0},
		"0.0 Gbit":                {poll.LinkOther, 0},
		"12.0 Gbit and then some": {poll.LinkOther, 0},
	} {
		if state, rate := link(said); state != want.state || rate != want.rate {
			t.Errorf("link(%q) = %s at %v, want %s at %v", said, state, rate, want.state, want.rate)
		}
	}
}

// The kernel's words for a state go on the page in small letters with underscores, so a label reads the same whichever of its tables a word came
// from.
func TestTheKernelsWordsAsThePageWritesThem(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]string{"OK": "ok", "not installed": "not_installed", "non-critical": "non_critical", "maintenance mode": "maintenance_mode", "running": "running", "transport-offline": "transport_offline", "created-blocked": "created_blocked", "": ""} {
		if got := word(said); got != want {
			t.Errorf("word(%q) = %q, want %q", said, got, want)
		}
	}
}

// An address is written the one way everywhere, sixteen small hex digits, so it can be matched against what another tool prints.
func TestAnAddressHasNothingBeforeIt(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]string{"0x500062b000000001": "500062b000000001", "0x5000CCA000000003": "5000cca000000003", "500062b000000001": "500062b000000001", "": ""} {
		if got := address(said); got != want {
			t.Errorf("address(%q) = %q, want %q", said, got, want)
		}
	}
}

// A disk's world wide name is what joins it to what other tools say of the same disk, so it is given the way they give it: hex and nothing else.
// The kernel names a disk with no such name by its maker and model instead, which is not one.
func TestAWorldWideName(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]string{"naa.5000cca000000041": "5000cca000000041", "NAA.5000CCA000000041": "5000cca000000041", "eui.0025385b21404321": "0025385b21404321", "t10.ATA     Samsung SSD 870": "", "": ""} {
		if got := wwn(said); got != want {
			t.Errorf("wwn(%q) = %q, want %q", said, got, want)
		}
	}
}

// A serial number comes padded with spaces, before it on the disks seen so far, inside a page that says how long it is. A page that is some other
// page, or cut short, has no serial number to give rather than a wrong one.
func TestTheSerialNumberInItsPage(t *testing.T) {
	t.Parallel()

	page := func(length byte, body string) []byte { return append([]byte{0x00, 0x80, 0x00, length}, body...) }

	for name, c := range map[string]struct {
		page []byte
		want string
	}{
		"padded before":           {page(16, "        Z0000001"), "Z0000001"},
		"padded after":            {page(16, "Z0000001        "), "Z0000001"},
		"shorter than it says":    {page(16, "Z0000001"), "Z0000001"},
		"longer than it says":     {page(8, "Z0000001junk"), "Z0000001"},
		"all spaces":              {page(24, "                        "), ""},
		"padded with nothing":     {page(10, "Z0000001\x00\x00"), "Z0000001"},
		"not text":                {page(4, "Z1\xff\xfe"), "Z1"},
		"another page":            {[]byte{0x00, 0x83, 0x00, 0x08, 'Z', '0', '0', '0', '0', '0', '0', '1'}, ""},
		"cut off before its body": {[]byte{0x00, 0x80}, ""},
		"nothing at all":          {nil, ""},
	} {
		if got := serial(c.page); got != c.want {
			t.Errorf("%s: serial = %q, want %q", name, got, c.want)
		}
	}
}

// A card's own link to the machine is given in the kernel's words too, in gigatransfers a second for each lane. One it does not know is 0, which
// is left off the page rather than shown as a link that is down.
func TestTheSpeedOfACardsOwnLink(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]float64{"16.0 GT/s PCIe": 16, "8.0 GT/s PCIe": 8, "5.0 GT/s PCIe": 5, "2.5 GT/s PCIe": 2.5, "5 GT/s": 5, "32.0 GT/s PCIe": 32, "Unknown": 0, "Unknown speed": 0, "": 0, "-1 GT/s": 0} {
		if got := transfers(said); got != want {
			t.Errorf("transfers(%q) = %v, want %v", said, got, want)
		}
	}
}

// The version of a card's settings is written as Broadcom's own tools write it, so it can be held against their release notes. What is not in the
// kernel's usual form is given as it is.
func TestTheVersionOfACardsSettings(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]string{"2001000bh": "32.01.00.11", "2001000eh": "32.01.00.14", "0e000300h": "14.00.03.00", "2001000b": "2001000b", "zz01000bh": "zz01000bh", "": ""} {
		if got := nvdata(said); got != want {
			t.Errorf("%q is %q, want %q", said, got, want)
		}
	}
}

// What the maker put in a device's description is text, with the padding and anything that is not text left out.
func TestTheMakersOwnPartOfADescription(t *testing.T) {
	t.Parallel()

	head := make([]byte, 36)
	for name, c := range map[string]struct {
		inquiry []byte
		want    string
	}{
		"a firmware version":      {slices.Concat(head, []byte("x40-66.16.11.0      ")), "x40-66.16.11.0"},
		"with bytes that are not": {slices.Concat(head, []byte("x28\x00\x01-66.16.13.0\xff    ")), "x28-66.16.13.0"},
		"too short to have any":   {head, ""},
		"nothing but padding":     {make([]byte, 96), ""},
	} {
		if got := vendorSpecific(c.inquiry); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
