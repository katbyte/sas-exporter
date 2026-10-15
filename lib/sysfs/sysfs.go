// Package sysfs asks the kernel about the machine's SAS cards by reading what it lays out under /sys, which needs no root and opens no device.
//
// The kernel does the talking. Reading a phy's error count makes it ask the card or the expander for that count there and then, and reading a
// slot's state makes it fetch the enclosure's whole status page, so none of this is read more often than the poller asks.
package sysfs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/katbyte/sas-exporter/lib/mpt"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/ses"
)

// The kernel's names for the directories read here.
const (
	classDir     = "class"
	moduleDir    = "module"
	sasHostDir   = "sas_host"
	scsiHostDir  = "scsi_host"
	sasPhyDir    = "sas_phy"
	sasDeviceDir = "sas_device"
	expanderDir  = "sas_expander"
	enclosureDir = "enclosure"
	blockDir     = "block"

	phyPrefix      = "phy-"
	portPrefix     = "port-"
	expanderPrefix = "expander-"
	endPrefix      = "end_device-"
	targetPrefix   = "target"
	seatPrefix     = "enclosure_device:"
)

// running is the kernel's word for a card, or a device, in service.
const running = "running"

// enclosureKind is the kernel's number for an enclosure's own processor among the kinds of device there are.
const enclosureKind = "13"

// slow is how long a read of an error count may take before the card is taken to have stopped answering. One that is refused comes back at once;
// one the card never answers takes the kernel ten seconds to give up on, and it may then reset the card.
const slow = 2 * time.Second

// most is how many things that could not be read are named in an error, with the rest only counted.
const most = 3

// countFiles are the files a phy's four error counts are in, in the order PhyErrors holds them.
var countFiles = [4]string{"invalid_dword_count", "running_disparity_error_count", "loss_of_dword_sync_count", "phy_reset_problem_count"}

// Kernel reads what the kernel says of the machine's SAS cards from its /sys, and asks the hardware itself for what the kernel does not keep.
type Kernel struct {
	root    string
	devices Devices
	// read reads one file whole: os.ReadFile, or a test's own that refuses or is slow
	read func(path string) ([]byte, error)

	mu sync.Mutex
	// refused is the cards that failed to give their temperature, and how: each is asked once and not again, since a card that does not answer is
	// reset by its driver
	refused map[string]error
	// limitless is the enclosures that would not give the page of limits they hold their sensors to, which not every enclosure has: each is
	// asked for it once and not again
	limitless map[string]bool
}

// Devices is how the hardware itself is asked for what is not in /sys. Either may be nil, for something that is not to be asked.
type Devices struct {
	// Diagnostic is one page of an enclosure's diagnostics, asked of the SCSI device of that name: sg26.
	Diagnostic func(ctx context.Context, device string, page byte) ([]byte, error)
	// CardPage is the page a card keeps its own temperature in, asked of the card whose driver knows it by that number, and nothing for a card
	// that has no such page.
	CardPage func(ctx context.Context, driver string, id uint32) ([]byte, error)
}

// New is the kernel whose /sys is at root, with those ways of asking the hardware itself.
func New(root string, devices Devices) *Kernel {
	return &Kernel{root: root, devices: devices, read: os.ReadFile, refused: map[string]error{}, limitless: map[string]bool{}}
}

// text is what one file holds, less the padding around it.
func (k *Kernel) text(path ...string) (string, error) {
	raw, err := k.read(filepath.Join(path...))
	if err != nil {
		return "", err
	}

	return clean(string(raw)), nil
}

// attrs reads the files of one directory, and remembers the first that could not be read so a run of them needs one check at the end.
type attrs struct {
	k   *Kernel
	dir string
	err error
}

// text is what a file every driver has holds.
func (a *attrs) text(name string) string {
	value, err := a.k.text(a.dir, name)
	if err != nil && a.err == nil {
		a.err = err
	}

	return value
}

// optional is text for a file not every driver or kernel has: nothing when it is not there.
func (a *attrs) optional(name string) string {
	value, err := a.k.text(a.dir, name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && a.err == nil {
		a.err = err
	}

	return value
}

// count is the number in a file not every driver or kernel has, written in tens or with 0x before it in sixteens, and nil when it is not there.
func (a *attrs) count(name string) *uint64 {
	value := a.optional(name)
	if value == "" {
		return nil
	}

	n, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		if a.err == nil {
			a.err = fmt.Errorf("%s holds %q, which is not a count", filepath.Join(a.dir, name), value)
		}

		return nil
	}

	return &n
}

// HBAs is the cards the kernel has now. A kernel with no driver for one loaded has nowhere to list them, which is no cards and not a failure.
func (k *Kernel) HBAs(ctx context.Context) ([]poll.HBA, error) {
	entries, err := os.ReadDir(filepath.Join(k.root, classDir, sasHostDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing the SAS cards: %w", err)
	}

	hbas := make([]poll.HBA, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		hba, err := k.hba(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}

		hbas = append(hbas, hba)
	}

	return hbas, nil
}

// hostDir is the card's own directory, which everything hanging off it is under.
func (k *Kernel) hostDir(name string) (string, error) {
	return filepath.EvalSymlinks(filepath.Join(k.root, classDir, sasHostDir, name, "device"))
}

// hba is what the kernel says of one card. Only whether it is in service is something every driver says; the rest is there for the cards whose
// driver gives it, Broadcom's among them.
func (k *Kernel) hba(name string) (poll.HBA, error) {
	dir, err := k.hostDir(name)
	if err != nil {
		return poll.HBA{}, err
	}

	a := attrs{k: k, dir: filepath.Join(dir, scsiHostDir, name)}
	hba := poll.HBA{
		Name:       name,
		SASAddress: address(a.optional("host_sas_address")),
		Model:      a.optional("board_name"),
		Firmware:   a.optional("version_fw"),
		BIOS:       a.optional("version_bios"),
		Driver:     a.optional("proc_name"),
		Running:    a.text("state") == running,
		Resets:     a.count("ioc_reset_count"),
	}
	if a.err != nil {
		return poll.HBA{}, a.err
	}

	if hba.Driver != "" {
		driver := attrs{k: k, dir: filepath.Join(k.root, moduleDir, hba.Driver)}
		hba.DriverVersion = driver.optional("version")
	}

	if hba.SASAddress == "" {
		hba.SASAddress = k.firstPhyAddress(dir)
	}

	hba.Slot, err = k.slot(filepath.Dir(dir))

	return hba, err
}

// slot is a card's own link to the machine, which the kernel keeps beside the card, one directory up. A card it keeps no such thing for has none.
func (k *Kernel) slot(dir string) (*poll.PCIeLink, error) {
	a := attrs{k: k, dir: dir}
	width, most := a.count("current_link_width"), a.count("max_link_width")
	if width == nil || a.err != nil {
		return nil, a.err
	}

	link := &poll.PCIeLink{Speed: transfers(a.optional("current_link_speed")), MaximumSpeed: transfers(a.optional("max_link_speed")), Width: int(*width)} //nolint:gosec // G115: a link is at most 32 lanes wide
	if most != nil {
		link.MaximumWidth = int(*most) //nolint:gosec // G115: a link is at most 32 lanes wide
	}

	return link, a.err
}

// firstPhyAddress is a card's address as its first phy gives it, for a driver that does not say it of the card: every phy of a card carries the
// card's own address.
func (k *Kernel) firstPhyAddress(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), phyPrefix) {
			a := attrs{k: k, dir: filepath.Join(dir, entry.Name(), sasPhyDir, entry.Name())}

			return address(a.optional("sas_address"))
		}
	}

	return ""
}

// node is a card or an expander as the kernel lays it out: a directory of phys, and of ports that each lead on to an expander or a device.
type node struct {
	dir string
	// name is the expander's name, and empty for the card itself
	name string
	// lanes is how many phys of the node above lead here, and 0 for the card
	lanes int
	phys  []string
	ports []port
}

// port is one or more phys of a node acting as one link, and what is on the other end of it.
type port struct {
	phys      []string
	expanders []node
	ends      []end
}

// end is the end of a line: a device, or an enclosure's own processor.
type end struct {
	dir  string
	name string
}

// tree is everything under one card or expander, as directories. Nothing in it has been read yet, so making it costs the card nothing.
func (k *Kernel) tree(dir, name string, lanes int) (node, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return node{}, err
	}

	n := node{dir: dir, name: name, lanes: lanes}
	for _, entry := range entries {
		switch {
		case !entry.IsDir():
			continue
		case strings.HasPrefix(entry.Name(), phyPrefix):
			n.phys = append(n.phys, entry.Name())
		case strings.HasPrefix(entry.Name(), portPrefix):
			p, err := k.branch(filepath.Join(dir, entry.Name()))
			if err != nil {
				return node{}, err
			}

			n.ports = append(n.ports, p)
		}
	}

	return n, nil
}

// branch is one port as its directory lays it out: links to the phys that make it up, beside the expander or the device they lead to.
func (k *Kernel) branch(dir string) (port, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return port{}, err
	}

	var p port
	var expanders []string
	for _, entry := range entries {
		switch {
		case strings.HasPrefix(entry.Name(), phyPrefix):
			p.phys = append(p.phys, entry.Name())
		case entry.IsDir() && strings.HasPrefix(entry.Name(), endPrefix):
			p.ends = append(p.ends, end{dir: filepath.Join(dir, entry.Name()), name: entry.Name()})
		case entry.IsDir() && strings.HasPrefix(entry.Name(), expanderPrefix):
			expanders = append(expanders, entry.Name())
		}
	}

	for _, name := range expanders {
		n, err := k.tree(filepath.Join(dir, name), name, len(p.phys))
		if err != nil {
			return port{}, err
		}

		p.expanders = append(p.expanders, n)
	}

	return p, nil
}

// Links is what hangs off one card. An error means none of it could be read; a phy that would not give its error counts is in Unread, with
// everything else as it was read.
func (k *Kernel) Links(ctx context.Context, hba string) (poll.Links, error) {
	dir, err := k.hostDir(hba)
	if err != nil {
		return poll.Links{}, err
	}

	top, err := k.tree(dir, "", 0)
	if err != nil {
		return poll.Links{}, err
	}

	r := reading{k: k}
	if err := r.node(ctx, &top); err != nil {
		return poll.Links{}, err
	}

	r.links.Unread = r.unread()

	return r.links, nil
}

// reading gathers what hangs off one card as its tree is gone through.
type reading struct {
	k     *Kernel
	links poll.Links
	// failed is what could not be read, and quiet says the card took so long over an error count that it is asked for no more of them this round
	failed []error
	quiet  bool
}

// unread is what could not be read as one error: the first few by name, and how many more there were.
func (r *reading) unread() error {
	if len(r.failed) <= most {
		return errors.Join(r.failed...)
	}

	return errors.Join(append(slices.Clone(r.failed[:most]), fmt.Errorf("and %d more", len(r.failed)-most))...)
}

// node reads one card or expander: its phys, then the devices and the further expanders its ports lead to.
func (r *reading) node(ctx context.Context, n *node) error {
	// what each phy leads to, by the phy's name: a port lists its phys beside what is on the other end
	attached := map[string]poll.Attached{}
	for _, p := range n.ports {
		kind := poll.AttachedNone
		switch {
		case len(p.expanders) > 0:
			kind = poll.AttachedExpander
		case len(p.ends) > 0:
			kind = poll.AttachedDevice
		}

		for _, name := range p.phys {
			attached[name] = kind
		}
	}

	numbers := map[string]int{}
	phys := make([]poll.Phy, 0, len(n.phys))
	for _, name := range n.phys {
		if err := ctx.Err(); err != nil {
			return err
		}

		phy, err := r.phy(filepath.Join(n.dir, name, sasPhyDir, name), n.name)
		if err != nil {
			return err
		}

		phy.Attached = poll.AttachedNone
		if kind, listed := attached[name]; listed {
			phy.Attached = kind
		}

		numbers[name] = phy.Number
		phys = append(phys, phy)
	}

	slices.SortFunc(phys, func(a, b poll.Phy) int { return cmp.Compare(a.Number, b.Number) })
	upstream(phys, n.lanes)
	r.links.Phys = append(r.links.Phys, phys...)

	for i := range n.ports {
		if err := r.port(ctx, n, &n.ports[i], numbers); err != nil {
			return err
		}
	}

	return nil
}

// upstream marks the phys of an expander that lead back towards the card. The kernel lists what is below a phy and never what is above it, so they
// are found by what is left: as many of the lowest-numbered phys with a link and nothing listed below as the port that leads here is wide. An
// expander's phys for its own inner workings also have a link and nothing below, and are numbered after the ones with a connector.
func upstream(phys []poll.Phy, lanes int) {
	for i := range phys {
		if lanes == 0 {
			return
		}

		if phys[i].Link == poll.LinkUp && phys[i].Attached == poll.AttachedNone {
			phys[i].Attached = poll.AttachedUpstream
			lanes--
		}
	}
}

// port reads what one port leads to: a device, or an expander and everything below it. A device's link is on the lowest-numbered phy of its port.
func (r *reading) port(ctx context.Context, n *node, p *port, numbers map[string]int) error {
	at := -1
	for _, name := range p.phys {
		if number, read := numbers[name]; read && (at < 0 || number < at) {
			at = number
		}
	}

	for _, e := range p.ends {
		if err := ctx.Err(); err != nil {
			return err
		}

		device, is, err := r.k.device(e, n.name, at)
		if err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}

		if is {
			r.links.Devices = append(r.links.Devices, device)
		}
	}

	for i := range p.expanders {
		below := &p.expanders[i]

		expander, err := r.k.expander(below)
		if err != nil {
			return fmt.Errorf("%s: %w", below.name, err)
		}

		r.links.Expanders = append(r.links.Expanders, expander)

		if err := r.node(ctx, below); err != nil {
			return err
		}
	}

	return nil
}

// phy reads one phy: its number, its link and its error counts.
func (r *reading) phy(dir, expander string) (poll.Phy, error) {
	a := attrs{k: r.k, dir: dir}
	identifier, negotiated, maximum := a.text("phy_identifier"), a.text("negotiated_linkrate"), a.optional("maximum_linkrate")
	if a.err != nil {
		return poll.Phy{}, a.err
	}

	number, err := strconv.Atoi(identifier)
	if err != nil {
		return poll.Phy{}, fmt.Errorf("%s says it is phy %q, which is not a number", dir, identifier)
	}

	state, rate := link(negotiated)
	_, ceiling := link(maximum)

	return poll.Phy{Expander: expander, Number: number, Link: state, Rate: rate, Maximum: ceiling, Errors: r.counts(dir)}, nil
}

// counts reads a phy's four error counts, each of which the kernel fetches from the card or the expander as its file is read. A phy that keeps
// none is refused at once and is left without. A read that takes seconds is a card that has stopped answering, and is the last one asked of it this
// round: every one more would wait as long, and the kernel may reset the card over each.
func (r *reading) counts(dir string) *poll.PhyErrors {
	if r.quiet {
		return nil
	}

	var read [len(countFiles)]uint64
	for i, name := range countFiles {
		path := filepath.Join(dir, name)

		start := time.Now()
		value, err := r.k.text(path)
		took := time.Since(start)

		if err == nil {
			if read[i], err = strconv.ParseUint(value, 10, 64); err != nil {
				err = fmt.Errorf("%s holds %q, which is not a count", path, value)
			}
		}

		switch {
		case err == nil:
			continue
		case took >= slow:
			r.quiet = true
			r.failed = append(r.failed, fmt.Errorf("%s took %s and failed, so the card was asked for no more error counts: %w", path, took.Round(time.Millisecond), err))
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.EINVAL):
			// the phy keeps no counts: one inside an expander, or with nothing wired to it
		default:
			r.failed = append(r.failed, err)
		}

		return nil
	}

	return &poll.PhyErrors{InvalidDwords: read[0], RunningDisparity: read[1], LossOfDwordSync: read[2], ResetProblems: read[3]}
}

// expander is what one expander says of itself.
func (k *Kernel) expander(n *node) (poll.Expander, error) {
	own := attrs{k: k, dir: filepath.Join(n.dir, expanderDir, n.name)}
	device := attrs{k: k, dir: filepath.Join(n.dir, sasDeviceDir, n.name)}

	expander := poll.Expander{
		Name:       n.name,
		SASAddress: address(device.optional("sas_address")),
		Vendor:     own.optional("vendor_id"),
		Product:    own.optional("product_id"),
		Revision:   own.optional("product_rev"),
	}

	return expander, cmp.Or(own.err, device.err)
}

// scsiDir is the directory the kernel keeps a device's own description in, under the end of its line, and "" for a device it has not looked at yet.
func scsiDir(e end) (string, error) {
	targets, err := os.ReadDir(e.dir)
	if err != nil {
		return "", err
	}

	for _, target := range targets {
		if !target.IsDir() || !strings.HasPrefix(target.Name(), targetPrefix) {
			continue
		}

		devices, err := os.ReadDir(filepath.Join(e.dir, target.Name()))
		if err != nil {
			return "", err
		}

		for _, device := range devices {
			// a device is named for the four numbers that place it, 1:0:5:0
			if device.IsDir() && strings.Count(device.Name(), ":") == 3 {
				return filepath.Join(e.dir, target.Name(), device.Name()), nil
			}
		}
	}

	return "", nil
}

// device is what the kernel says of the device at the end of one line, on that phy of that expander. It is false for an enclosure's own processor,
// which is reported as an enclosure, and for a device the kernel has not looked at yet.
func (k *Kernel) device(e end, expander string, phy int) (poll.Device, bool, error) {
	dir, err := scsiDir(e)
	if err != nil || dir == "" {
		return poll.Device{}, false, err
	}

	a := attrs{k: k, dir: dir}
	if a.text("type") == enclosureKind {
		return poll.Device{}, false, a.err
	}

	line := attrs{k: k, dir: filepath.Join(e.dir, sasDeviceDir, e.name)}
	device := poll.Device{
		SASAddress: address(line.text("sas_address")),
		WWN:        wwn(a.optional("wwid")),
		Vendor:     a.optional("vendor"),
		Model:      a.optional("model"),
		Revision:   a.optional("rev"),
		Expander:   expander,
		Phy:        phy,
		State:      word(a.text("state")),
		Sent:       a.count("iorequest_cnt"),
		Completed:  a.count("iodone_cnt"),
		Errors:     a.count("ioerr_cnt"),
		Timeouts:   a.count("iotmo_cnt"),
	}
	if err := cmp.Or(a.err, line.err); err != nil {
		return poll.Device{}, false, err
	}

	// the page with the serial number is not text, so it is read as it is; a device with none has no such file
	if page, err := k.read(filepath.Join(dir, "vpd_pg80")); err == nil {
		device.Serial = serial(page)
	}

	if disks, err := os.ReadDir(filepath.Join(dir, blockDir)); err == nil && len(disks) > 0 {
		device.Disk = disks[0].Name()
	}

	device.Enclosure, device.Slot, err = k.seat(dir)

	return device, true, err
}

// seat is the enclosure and the slot a device sits in, as the enclosure tells the kernel, which then links the device to its slot. A device no
// enclosure claims has no such link and no seat.
func (k *Kernel) seat(dir string) (enclosure, slot string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}

	for _, entry := range entries {
		named, found := strings.CutPrefix(entry.Name(), seatPrefix)
		if !found {
			continue
		}

		target, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", "", err
		}

		at := filepath.Join(dir, target)
		a := attrs{k: k, dir: at}

		return filepath.Base(filepath.Dir(at)), slotNumber(a.optional("slot"), named), a.err
	}

	return "", "", nil
}

// slotNumber is a slot's number as the enclosure gives it, or the name the enclosure calls the slot by when it gives no number.
func slotNumber(given, named string) string {
	if given == "" || strings.HasPrefix(given, "-") {
		return named
	}

	return given
}

// Enclosures is the enclosures on one card and what each says of its slots.
func (k *Kernel) Enclosures(ctx context.Context, hba string) ([]poll.Enclosure, error) {
	dir, err := k.hostDir(hba)
	if err != nil {
		return nil, err
	}

	top, err := k.tree(dir, "", 0)
	if err != nil {
		return nil, err
	}

	enclosures, err := k.enclosuresBelow(ctx, &top)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(enclosures, func(a, b poll.Enclosure) int { return cmp.Compare(a.Name, b.Name) })

	return enclosures, nil
}

// enclosuresBelow is the enclosures below one card or expander: each is the processor at the end of a line that the kernel has an enclosure for.
func (k *Kernel) enclosuresBelow(ctx context.Context, n *node) ([]poll.Enclosure, error) {
	var out []poll.Enclosure
	for i := range n.ports {
		for _, e := range n.ports[i].ends {
			dir, err := scsiDir(e)
			if err != nil {
				return nil, err
			}
			if dir == "" {
				continue
			}

			entries, err := os.ReadDir(filepath.Join(dir, enclosureDir))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				enclosure, err := k.enclosure(ctx, dir, entry.Name(), n.name)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", entry.Name(), err)
				}

				out = append(out, enclosure)
			}
		}

		for j := range n.ports[i].expanders {
			below, err := k.enclosuresBelow(ctx, &n.ports[i].expanders[j])
			if err != nil {
				return nil, err
			}

			out = append(out, below...)
		}
	}

	return out, nil
}

// enclosure is what one enclosure says of itself and of each of its slots. Each of a slot's three readings has the kernel fetch the enclosure's
// status afresh.
func (k *Kernel) enclosure(ctx context.Context, scsi, name, expander string) (poll.Enclosure, error) {
	dir := filepath.Join(scsi, enclosureDir, name)
	device, own := attrs{k: k, dir: scsi}, attrs{k: k, dir: dir}

	enclosure := poll.Enclosure{
		Name:     name,
		ID:       address(own.optional("id")),
		Vendor:   device.optional("vendor"),
		Product:  device.optional("model"),
		Revision: device.optional("rev"),
		Expander: expander,
	}
	if err := cmp.Or(device.err, own.err); err != nil {
		return poll.Enclosure{}, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return poll.Enclosure{}, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return poll.Enclosure{}, err
		}

		a := attrs{k: k, dir: filepath.Join(dir, entry.Name())}
		given := a.optional("slot")
		// a directory with no slot number in it is the kernel's own housekeeping and not a slot
		if given == "" && a.err == nil {
			continue
		}

		slot := poll.Slot{Number: slotNumber(given, entry.Name()), Status: word(a.text("status")), Fault: lit(a.text("fault")), Locate: lit(a.text("locate"))}
		if a.err != nil {
			return poll.Enclosure{}, a.err
		}

		enclosure.Slots = append(enclosure.Slots, slot)
	}

	slices.SortFunc(enclosure.Slots, func(a, b poll.Slot) int {
		return cmp.Or(cmp.Compare(len(a.Number), len(b.Number)), cmp.Compare(a.Number, b.Number))
	})

	return enclosure, nil
}

// lit says whether one of a slot's lights is on: the kernel writes 0 for off, and a number for which of the ways it can be on.
func lit(value string) bool {
	return value != "" && value != "0"
}

// Sensors is what one card and its enclosures measure of themselves, asked of the hardware itself. What there is no leave to ask, or no way to ask
// here, is said in Unavailable and is not a failure: the program runs without it. What was asked and would not answer is in Unread.
func (k *Kernel) Sensors(ctx context.Context, hba string) (poll.Sensors, error) {
	dir, err := k.hostDir(hba)
	if err != nil {
		return poll.Sensors{}, err
	}

	var s poll.Sensors
	var unread []error
	var unavailable []string

	// note files what went wrong with one kind of thing under what it was: kept out, or asked and not answered
	note := func(what string, err error) {
		switch {
		case err == nil:
		case kept(err):
			unavailable = append(unavailable, what+": "+err.Error())
		default:
			unread = append(unread, fmt.Errorf("%s: %w", what, err))
		}
	}

	if k.devices.CardPage != nil {
		s.Temperatures, err = k.temperatures(ctx, hba, dir)
		note("the card's temperature", err)
	}

	if k.devices.Diagnostic != nil {
		top, err := k.tree(dir, "", 0)
		if err != nil {
			return poll.Sensors{}, err
		}

		s.Enclosures, err = k.sensorsBelow(ctx, &top)
		note("the enclosures' sensors", err)
	}

	slices.SortFunc(s.Enclosures, func(a, b poll.EnclosureSensors) int { return cmp.Compare(a.Name, b.Name) })
	s.Unread, s.Unavailable = errors.Join(unread...), strings.Join(unavailable, "; ")

	return s, nil
}

// kept says an error is being kept out rather than something going wrong: no leave to open a device, no such device where this runs, or a
// machine this cannot be asked on at all.
func kept(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, errors.ErrUnsupported) || errors.Is(err, errNotAgain)
}

// errNotAgain is a card that failed to give its temperature once and is not asked again.
var errNotAgain = errors.New("not asked again until the program is started again, so as not to trouble the card")

// temperatures is a card's own temperatures, asked of the card through its driver. A card whose driver gives it no number cannot be asked, and
// has none. A card that fails to answer is asked the once: the driver resets a card that does not answer, and that must not happen every minute.
func (k *Kernel) temperatures(ctx context.Context, hba, dir string) ([]poll.Temperature, error) {
	a := attrs{k: k, dir: filepath.Join(dir, scsiHostDir, hba)}
	driver, id := a.optional("proc_name"), a.count("unique_id")
	if a.err != nil || driver == "" || id == nil {
		return nil, a.err
	}

	k.mu.Lock()
	before := k.refused[hba]
	k.mu.Unlock()
	if before != nil {
		return nil, fmt.Errorf("%w (%w)", errNotAgain, before)
	}

	page, err := k.devices.CardPage(ctx, driver, uint32(*id)) //nolint:gosec // G115: the driver numbers its cards from 0
	if err != nil {
		// being kept out is not the card failing, and neither is being stopped part way, nor a driver too busy to pass the question on
		if !kept(err) && !errors.Is(err, syscall.EAGAIN) && ctx.Err() == nil {
			k.mu.Lock()
			k.refused[hba] = err
			k.mu.Unlock()
		}

		return nil, err
	}

	var out []poll.Temperature
	for _, t := range mpt.Temperatures(page) {
		sensor := poll.SensorChip
		if t.Board {
			sensor = poll.SensorBoard
		}

		out = append(out, poll.Temperature{Sensor: sensor, Celsius: t.Celsius})
	}

	return out, nil
}

// sensorsBelow is what each enclosure below one card or expander says of the things in it. The first one that cannot be asked stops the rest
// being asked: they are opened the same way and would be refused the same way.
func (k *Kernel) sensorsBelow(ctx context.Context, n *node) ([]poll.EnclosureSensors, error) {
	var out []poll.EnclosureSensors
	for i := range n.ports {
		for _, e := range n.ports[i].ends {
			dir, err := scsiDir(e)
			if err != nil {
				return out, err
			}
			if dir == "" {
				continue
			}

			a := attrs{k: k, dir: dir}
			if a.text("type") != enclosureKind {
				if a.err != nil {
					return out, a.err
				}

				continue
			}

			sensors, err := k.enclosureSensors(ctx, dir)
			if err != nil {
				return out, fmt.Errorf("%s: %w", filepath.Base(dir), err)
			}

			out = append(out, sensors)
		}

		for j := range n.ports[i].expanders {
			below, err := k.sensorsBelow(ctx, &n.ports[i].expanders[j])
			out = append(out, below...)
			if err != nil {
				return out, err
			}
		}
	}

	return out, nil
}

// enclosureSensors asks one enclosure for the pages that describe it and reads everything in it that is not a slot. The enclosure is
// reached through the device the kernel's generic SCSI driver makes for it, which the kernel names beside the enclosure.
func (k *Kernel) enclosureSensors(ctx context.Context, dir string) (poll.EnclosureSensors, error) {
	generic, err := os.Readlink(filepath.Join(dir, "generic"))
	if err != nil {
		return poll.EnclosureSensors{}, fmt.Errorf("the kernel has made no device to ask it through: %w", err)
	}

	var pages [3][]byte
	for i, page := range []byte{ses.PageConfiguration, ses.PageStatus, ses.PageNames} {
		if pages[i], err = k.devices.Diagnostic(ctx, filepath.Base(generic), page); err != nil {
			return poll.EnclosureSensors{}, err
		}
	}

	enclosure, err := ses.Parse(pages[0], pages[1], pages[2])
	if err != nil {
		return poll.EnclosureSensors{}, err
	}
	k.limits(ctx, dir, filepath.Base(generic), &enclosure, pages[0])

	out := poll.EnclosureSensors{
		Name:       filepath.Base(dir),
		Conditions: poll.Conditions{Info: enclosure.Info, NonCritical: enclosure.NonCritical, Critical: enclosure.Critical, Unrecoverable: enclosure.Unrecoverable},
	}

	named := map[string]bool{}
	for i := range enclosure.Elements {
		e := &enclosure.Elements[i]
		// the kernel already reports on the slots, without the enclosure being asked
		if e.Kind.Slot() {
			continue
		}

		element := poll.Element{Kind: e.Kind.String(), Name: elementName(e, named), Status: e.StatusWord(), Fault: e.Fault()}
		if rpm, ok := e.RPM(); ok {
			element.RPM = &rpm
		}
		if celsius, ok := e.Celsius(); ok {
			element.Celsius = &celsius
		}
		if volts, ok := e.Volts(); ok {
			element.Volts = &volts
		}
		if amperes, ok := e.Amperes(); ok {
			element.Amperes = &amperes
		}
		if limits, ok := e.Limits(); ok {
			element.Limits = &poll.Limits{HighCritical: limits.HighCritical, HighWarning: limits.HighWarning, LowWarning: limits.LowWarning, LowCritical: limits.LowCritical}
		}
		for _, problem := range e.Problems() {
			element.Problems = append(element.Problems, poll.Problem{Name: problem.Name, Set: problem.Set})
		}

		out.Elements = append(out.Elements, element)
	}

	return out, nil
}

// limits asks an enclosure for the limits it holds its sensors to and keeps them with what Parse read. Not every enclosure has that page, and going
// without costs only the limits: nothing here fails, and an enclosure that will not give the page is not asked for it again.
func (k *Kernel) limits(ctx context.Context, dir, device string, enclosure *ses.Enclosure, configuration []byte) {
	k.mu.Lock()
	without := k.limitless[dir]
	k.mu.Unlock()
	if without {
		return
	}

	page, err := k.devices.Diagnostic(ctx, device, ses.PageLimits)
	if err == nil {
		err = enclosure.Limit(configuration, page)
	}
	// being stopped part way is not the enclosure refusing
	if err != nil && ctx.Err() == nil {
		k.mu.Lock()
		k.limitless[dir] = true
		k.mu.Unlock()
	}
}

// elementName is what one thing in an enclosure goes by: what the enclosure calls it, or its number among those of its kind when the enclosure
// calls it nothing. Two of a kind that the enclosure calls the same have their numbers added, so no two share a name.
func elementName(e *ses.Element, named map[string]bool) string {
	name := e.Name
	if name == "" {
		name = strconv.Itoa(e.Number)
	}

	if named[e.Kind.String()+"\x00"+name] {
		name += " " + strconv.Itoa(e.Number)
	}
	named[e.Kind.String()+"\x00"+name] = true

	return name
}
