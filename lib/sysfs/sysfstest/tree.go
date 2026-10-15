// Package sysfstest lays out a made-up /sys for tests: the directories and files the kernel would show for the SAS cards a test describes, or the
// ones saved from a real machine.
package sysfstest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The kernel's own files can be read by anyone, and so can these: the live test hands them to a container that runs as someone else.
const (
	dirMode  = 0o755
	fileMode = 0o644
)

// The speeds a test gives a phy, in the kernel's own words.
const (
	Rate12  = "12.0 Gbit"
	Rate6   = "6.0 Gbit"
	NoLink  = "Unknown"
	Failed  = "Link Rate failed"
	Offline = "Phy disabled"
)

// Tree is a made-up /sys under a directory of the test's own.
type Tree struct {
	t *testing.T
	// Root is where it is, for the program to be pointed at.
	Root string
	// addresses, cards, devices, expanders and targets count what has been made, so no two things get the same address or name
	addresses int
	cards     int
	devices   int
	expanders map[int]int
	targets   map[int]int
}

// New is an empty /sys: a machine with no SAS card, and no driver for one loaded.
func New(t *testing.T) *Tree {
	t.Helper()

	return &Tree{t: t, Root: tempRoot(t), expanders: map[int]int{}, targets: map[int]int{}}
}

// tempRoot is a directory of the test's own, by the name links into it resolve to: a temporary directory is itself reached through a link on some
// systems, and a test that compares paths needs the one name for a file.
func tempRoot(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// Write puts value in the file of that name under dir, making the directory if it is not there. An empty name makes the directory alone.
func (tr *Tree) Write(dir, name, value string) {
	tr.t.Helper()

	if err := os.MkdirAll(dir, dirMode); err != nil {
		tr.t.Fatal(err)
	}
	if name == "" {
		return
	}

	if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), fileMode); err != nil {
		tr.t.Fatal(err)
	}
}

// Remove takes away a file or a directory and everything in it: a driver that does not say something, or a card pulled out.
func (tr *Tree) Remove(path ...string) {
	tr.t.Helper()

	if err := os.RemoveAll(filepath.Join(path...)); err != nil {
		tr.t.Fatal(err)
	}
}

// Break makes a file one that cannot be read, by putting a directory where it was.
func (tr *Tree) Break(dir, name string) {
	tr.t.Helper()

	tr.Remove(dir, name)
	tr.Write(filepath.Join(dir, name), "", "")
}

// link makes a link at path that points to target, as the kernel's own do: relative to where the link is.
func (tr *Tree) link(target, path string) {
	tr.t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		tr.t.Fatal(err)
	}

	rel, err := filepath.Rel(filepath.Dir(path), target)
	if err != nil {
		tr.t.Fatal(err)
	}
	if err := os.Symlink(rel, path); err != nil {
		tr.t.Fatal(err)
	}
}

// address is an address nothing else in the tree has, written as the kernel writes one.
func (tr *Tree) address() string {
	tr.addresses++

	return fmt.Sprintf("0x5000000000%06x", tr.addresses)
}

// Node is a card or an expander in the tree: something phys, disks and further expanders are added to.
type Node struct {
	tree *Tree
	host int
	// Dir is the node's own directory, and Name the expander's name, empty for a card.
	Dir  string
	Name string
	// path is what the kernel's names for the node's phys and ports start with: 1 for a card, 1:0 for its first expander
	path    string
	address string
	ports   int
}

// Card adds a card the kernel calls host and that number: a Broadcom HBA 9500-16i in service, with the driver's version beside it. Write over
// what Attrs holds to make it another.
func (tr *Tree) Card(number int) *Node {
	tr.t.Helper()

	name := "host" + strconv.Itoa(number)
	dir := filepath.Join(tr.Root, "devices", "pci0000:00", fmt.Sprintf("0000:%02x:00.0", number), name)
	n := &Node{tree: tr, host: number, Dir: dir, path: strconv.Itoa(number), address: tr.address()}

	attrs := n.Attrs()
	for file, value := range map[string]string{"board_name": "HBA 9500-16i", "version_fw": "32.00.00.00", "version_bios": "09.27.00.00", "proc_name": "mpt3sas", "state": "running", "ioc_reset_count": "0", "host_sas_address": n.address, "unique_id": strconv.Itoa(tr.cards)} {
		tr.Write(attrs, file, value)
	}
	tr.Write(filepath.Join(tr.Root, "module", "mpt3sas"), "version", "48.100.00.00")
	// the driver numbers its cards from 0 in the order it finds them
	tr.cards++

	// the card's own link to the machine is kept beside it, one directory up
	for file, value := range map[string]string{"current_link_speed": "16.0 GT/s PCIe", "max_link_speed": "16.0 GT/s PCIe", "current_link_width": "8", "max_link_width": "8"} {
		tr.Write(n.Slot(), file, value)
	}

	class := filepath.Join(dir, "sas_host", name)
	tr.link(dir, filepath.Join(class, "device"))
	tr.link(dir, filepath.Join(attrs, "device"))
	tr.link(class, filepath.Join(tr.Root, "class", "sas_host", name))
	tr.link(attrs, filepath.Join(tr.Root, "class", "scsi_host", name))

	return n
}

// Slot is the directory the kernel keeps a card's own link to the machine in: how fast and how wide.
func (n *Node) Slot() string {
	return filepath.Dir(n.Dir)
}

// Attrs is the directory a card says what it is in: its model, its firmware, whether it is in service.
func (n *Node) Attrs() string {
	return filepath.Join(n.Dir, "scsi_host", "host"+strconv.Itoa(n.host))
}

// Class is where the kernel lists a card among the SAS cards it has, for a test that takes one away.
func (n *Node) Class() string {
	return filepath.Join(n.tree.Root, "class", "sas_host", "host"+strconv.Itoa(n.host))
}

// phyName is the kernel's name for one of the node's phys.
func (n *Node) phyName(number int) string {
	return fmt.Sprintf("phy-%s:%d", n.path, number)
}

// Phy adds a phy of that number whose link came up at rate, with the four error counts given, in the order the kernel lists them, or none counted.
// It gives back the directory the phy's files are in.
func (n *Node) Phy(number int, rate string, counts ...int) string {
	n.tree.t.Helper()

	name := n.phyName(number)
	dir := filepath.Join(n.Dir, name, "sas_phy", name)

	n.tree.Write(dir, "phy_identifier", strconv.Itoa(number))
	n.tree.Write(dir, "negotiated_linkrate", rate)
	n.tree.Write(dir, "maximum_linkrate", Rate12)
	n.tree.Write(dir, "sas_address", n.address)
	for i, file := range []string{"invalid_dword_count", "running_disparity_error_count", "loss_of_dword_sync_count", "phy_reset_problem_count"} {
		count := 0
		if i < len(counts) {
			count = counts[i]
		}
		n.tree.Write(dir, file, strconv.Itoa(count))
	}

	return dir
}

// Phys adds a run of phys, first to last, each with a link at rate and nothing counted.
func (n *Node) Phys(first, last int, rate string) {
	n.tree.t.Helper()

	for number := first; number <= last; number++ {
		n.Phy(number, rate)
	}
}

// port makes a port of these phys of the node, which must have been added, and gives back its directory and its number.
func (n *Node) port(phys ...int) (dir string, number int) {
	n.tree.t.Helper()

	number = n.ports
	n.ports++
	dir = filepath.Join(n.Dir, fmt.Sprintf("port-%s:%d", n.path, number))
	n.tree.Write(dir, "", "")

	for _, phy := range phys {
		name := n.phyName(phy)
		n.tree.link(filepath.Join(n.Dir, name), filepath.Join(dir, name))
		n.tree.link(dir, filepath.Join(n.Dir, name, "port"))
	}

	return dir, number
}

// Expander adds an expander below these phys of the node: a Supermicro SC846 backplane's, unless its files are written over.
func (n *Node) Expander(phys ...int) *Node {
	n.tree.t.Helper()

	port, _ := n.port(phys...)
	index := n.tree.expanders[n.host]
	n.tree.expanders[n.host]++

	name := fmt.Sprintf("expander-%d:%d", n.host, index)
	below := &Node{tree: n.tree, host: n.host, Dir: filepath.Join(port, name), Name: name, path: fmt.Sprintf("%d:%d", n.host, index), address: n.tree.address()}

	// the kernel pads these out with spaces, as the expander does
	n.tree.Write(below.Info(), "vendor_id", "SMC     ")
	n.tree.Write(below.Info(), "product_id", "SC846-P         ")
	n.tree.Write(below.Info(), "product_rev", "100b")
	n.tree.Write(filepath.Join(below.Dir, "sas_device", name), "sas_address", below.address)

	return below
}

// Info is the directory an expander says what it is in: its maker, its product and its revision.
func (n *Node) Info() string {
	return filepath.Join(n.Dir, "sas_expander", n.Name)
}

// end adds the end of a line below one phy of the node, with a device of that kind the kernel has looked at, and gives back the device's directory.
func (n *Node) end(phy int, kind string) string {
	n.tree.t.Helper()

	port, number := n.port(phy)
	name := fmt.Sprintf("end_device-%s:%d", n.path, number)
	target := n.tree.targets[n.host]
	n.tree.targets[n.host]++

	address := n.tree.address()
	n.tree.Write(filepath.Join(port, name, "sas_device", name), "sas_address", address)

	dir := filepath.Join(port, name, fmt.Sprintf("target%d:0:%d", n.host, target), fmt.Sprintf("%d:0:%d:0", n.host, target))
	for file, value := range map[string]string{"type": kind, "state": "running", "sas_address": address, "iorequest_cnt": "0x0", "iodone_cnt": "0x0", "ioerr_cnt": "0x0", "iotmo_cnt": "0x0"} {
		n.tree.Write(dir, file, value)
	}

	// the kernel makes every device another it can be sent commands through, and names that one beside it
	n.tree.devices++
	if err := os.Symlink("scsi_generic/sg"+strconv.Itoa(n.tree.devices), filepath.Join(dir, "generic")); err != nil {
		n.tree.t.Fatal(err)
	}

	return dir
}

// Disk adds a disk below one phy of the node, under the name the kernel gives it and with that serial number: a 20 TB Western Digital with nothing
// wrong with it. It gives back the directory the disk's files are in.
func (n *Node) Disk(phy int, name, serial string) string {
	n.tree.t.Helper()

	dir := n.end(phy, "0")
	for file, value := range map[string]string{"vendor": "WDC     ", "model": "WUH722020BL5204 ", "rev": "C54K", "wwid": "naa." + n.tree.address()[2:]} {
		n.tree.Write(dir, file, value)
	}
	n.tree.Write(filepath.Join(dir, "block", name), "", "")

	// the page with the serial number: what it is, how long, and the number padded out with spaces
	page := append([]byte{0x00, 0x80, 0x00, 0x10}, fmt.Sprintf("%16s", serial)...)
	if err := os.WriteFile(filepath.Join(dir, "vpd_pg80"), page, fileMode); err != nil {
		n.tree.t.Fatal(err)
	}

	return dir
}

// Shelf is an enclosure in the tree: something slots are added to.
type Shelf struct {
	tree *Tree
	// Dir is the enclosure's own directory, Name the kernel's name for it, and Device the name of the device it is asked about itself through.
	Dir    string
	Name   string
	Device string
}

// Enclosure adds an enclosure whose processor is below one phy of the node, a Supermicro SC846 unless its files are written over, with no slots yet.
func (n *Node) Enclosure(phy int) *Shelf {
	n.tree.t.Helper()

	dir := n.end(phy, "13")
	for file, value := range map[string]string{"vendor": "SMC     ", "model": "SC846-P         ", "rev": "100b"} {
		n.tree.Write(dir, file, value)
	}

	name := filepath.Base(dir)
	s := &Shelf{tree: n.tree, Dir: filepath.Join(dir, "enclosure", name), Name: name, Device: "sg" + strconv.Itoa(n.tree.devices)}
	n.tree.Write(s.Dir, "id", n.tree.address())
	n.tree.Write(s.Dir, "components", "0")
	// the kernel keeps a directory of its own beside the slots
	n.tree.Write(filepath.Join(s.Dir, "power"), "control", "auto")
	n.tree.link(s.Dir, filepath.Join(n.tree.Root, "class", "enclosure", name))

	return s
}

// Slot adds a slot of that number, with what the enclosure says of it in the kernel's words, "OK" or "not installed", and its lights off. disk is
// the directory of the disk that sits in it, as Disk gave it, or empty for a slot with none. It gives back the directory the slot's files are in.
func (s *Shelf) Slot(number int, status, disk string) string {
	s.tree.t.Helper()

	named := fmt.Sprintf("Slot%02d", number)
	dir := filepath.Join(s.Dir, named)
	for file, value := range map[string]string{"slot": strconv.Itoa(number), "status": status, "fault": "0", "locate": "0", "type": "array device"} {
		s.tree.Write(dir, file, value)
	}

	if disk != "" {
		s.tree.link(disk, filepath.Join(dir, "device"))
		s.tree.link(dir, filepath.Join(disk, "enclosure_device:"+named))
	}

	return dir
}
