// Package poll asks the kernel about the machine's SAS cards on a schedule and keeps the last good answers, so the metrics page is served from memory
// and a card that is slow to answer never holds up a scrape.
//
// The types say where a thing is by the kernel's names for it today, host1 and expander-1:0, which are the ones its own tools print. Those can change
// at a reboot or when a cable is moved, so each thing also carries the address that does not.
package poll

import (
	"context"
)

// Kernel is the questions asked of the kernel about the SAS cards in the machine. Every one of them only reads.
type Kernel interface {
	// HBAs is the cards the kernel has now and what it says of each, which costs the cards nothing.
	HBAs(ctx context.Context) ([]HBA, error)
	// Links is what hangs off one card, named as HBAs named it: its expanders, every phy with its link and error counts, and the devices at the ends.
	Links(ctx context.Context, hba string) (Links, error)
	// Enclosures is the enclosures on one card and what each says of its slots.
	Enclosures(ctx context.Context, hba string) ([]Enclosure, error)
	// Sensors is what one card and its enclosures measure of themselves: temperatures, fans, power supplies, voltages. The kernel keeps none of it,
	// so the card and the enclosures are asked themselves, which takes leave to open their devices.
	Sensors(ctx context.Context, hba string) (Sensors, error)
}

// HBA is one SAS card, as the kernel describes it.
type HBA struct {
	// Name is the kernel's name for the card today, host1, which is what it is asked by.
	Name string
	// SASAddress is the card's own address, which stays the same when its name changes.
	SASAddress    string
	Model         string
	Firmware      string
	BIOS          string
	Driver        string
	DriverVersion string
	// Serial is the card's own serial number, Assembly the maker's part number for the board, and Chip what it is built on: SAS3816.
	Serial   string
	Assembly string
	Chip     string
	// NVData is the version of the settings the card keeps in its own memory, and NVDataDefault of the ones it came with: the two differ on a card
	// whose settings were changed. MPI is the version of the language the card and its driver speak.
	NVData        string
	NVDataDefault string
	MPI           string
	// Running says the kernel has the card in service, and is not resetting it or taking it away.
	Running bool
	// Resets is how many times the card's firmware has been restarted since its driver loaded, and nil for a driver that keeps no count.
	Resets *uint64
	// Slot is the link between the card and the machine it is plugged into, and nil for a card the kernel says nothing of the kind about.
	Slot *PCIeLink
	// InFlight is how many commands the kernel had sent the card that had not come back when it was asked, and Queue the most it will have out with
	// the card at once. Each is nil for a driver that does not say.
	InFlight *uint64
	Queue    *uint64
}

// PCIeLink is a card's own link to the machine: how fast each of its lanes runs and how many there are, beside the most the card and its slot can
// do between them. A card running on fewer or slower lanes than that is in the wrong slot, or not seated.
type PCIeLink struct {
	// Speed and MaximumSpeed are in gigatransfers a second for each lane: 16 for a PCIe 4.0 link. 0 is a speed the kernel does not say.
	Speed        float64
	MaximumSpeed float64
	// Width and MaximumWidth are how many lanes.
	Width        int
	MaximumWidth int
}

// Links is what hangs off one card.
type Links struct {
	Expanders []Expander
	Phys      []Phy
	Devices   []Device
	// Unread is what the kernel would not say this time, and nil when it said everything: the error counts of a card that is being reset, say. The
	// rest is as it was read.
	Unread error
}

// Expander is one expander: the chip on a backplane that fans a card's lanes out to the slots.
type Expander struct {
	// Name is the kernel's name for the expander today, expander-1:0: the value of the expander label.
	Name string
	// SASAddress is the expander's own address, which stays the same when its name changes.
	SASAddress string
	Vendor     string
	Product    string
	Revision   string
	// ChipVendor, Chip and ChipRevision are the chip the expander is built on, as its maker numbers it: LSI, 562, 3.
	ChipVendor   string
	Chip         string
	ChipRevision string
}

// LinkState is what a phy says of its link.
type LinkState string

const (
	LinkUp LinkState = "up"
	// LinkDown is nothing on the other end, or nothing the phy can hear.
	LinkDown     LinkState = "down"
	LinkDisabled LinkState = "disabled"
	// LinkFailed is two ends that could not agree on a speed.
	LinkFailed LinkState = "failed"
	// LinkSpinupHold is a SATA disk waiting to be told it may spin up.
	LinkSpinupHold LinkState = "spinup_hold"
	// LinkOther is a word of the kernel's this program has none for.
	LinkOther LinkState = "other"
)

// Attached is what is on the other end of a phy's link.
type Attached string

const (
	// AttachedDevice is a disk, or anything else at the end of the line: an enclosure's own processor is one.
	AttachedDevice Attached = "device"
	// AttachedExpander is a further expander, away from the card.
	AttachedExpander Attached = "expander"
	// AttachedUpstream is the way back towards the card.
	AttachedUpstream Attached = "upstream"
	// AttachedNone is nothing the kernel lists, whatever the phy says of its link: an empty slot, or a phy inside the expander itself.
	AttachedNone Attached = "none"
)

// Phy is one end of one lane: a card or an expander has one for each lane it can drive.
type Phy struct {
	// Expander is the expander the phy is on, and empty for one of the card's own.
	Expander string
	Number   int
	Link     LinkState
	// Rate is the speed the link came up at, in gigabits a second as SAS speeds are always given, and 0 with no link. Maximum and Minimum are the
	// most and the least the phy is set to offer, and HardwareMaximum and HardwareMinimum the most and the least it can; each is 0 for a phy that
	// does not say.
	Rate            float64
	Maximum         float64
	Minimum         float64
	HardwareMaximum float64
	HardwareMinimum float64
	Attached        Attached
	// Errors is the phy's error counts, and nil for a phy that keeps none or would not give them this time.
	Errors *PhyErrors
}

// PhyErrors is what a phy has counted going wrong on its link since it was last reset. Any of them growing is a cable, a connector or a slot going
// bad.
type PhyErrors struct {
	// InvalidDwords is words that arrived garbled.
	InvalidDwords uint64
	// RunningDisparity is words that arrived with the wrong balance of ones and noughts, another sign of a noisy lane.
	RunningDisparity uint64
	// LossOfDwordSync is times the phy lost its place in what was arriving and had to find it again.
	LossOfDwordSync uint64
	// ResetProblems is times the link failed to come back after a reset.
	ResetProblems uint64
}

// Device is something at the end of a line that is not an enclosure's own processor: a disk, nearly always.
type Device struct {
	// SASAddress tells one device on a card from another, and is the one thing every device has.
	SASAddress string
	// Serial is the device's own serial number and Disk the kernel's name for it today, sda. Either may be empty.
	Serial string
	Disk   string
	// WWN is the device's world wide name as hex with nothing before it, and empty for a device with none.
	WWN      string
	Vendor   string
	Model    string
	Revision string
	// Expander and Phy say where the device's link is: that phy of that expander, or of the card itself when Expander is empty.
	Expander string
	Phy      int
	// Enclosure and Slot say where the device sits, as the enclosure tells it. Both are empty for a device in no enclosure's slot.
	Enclosure string
	Slot      string
	// State is what the kernel says the device is doing, in the page's words: running, blocked, offline.
	State string
	// Sent counts the commands the kernel has sent the device since it appeared and Completed the ones it has had back, so the difference is what
	// is out with the device now. Errors counts the ones that came back with anything but success, and Timeouts the ones never answered. Each is
	// nil where the kernel keeps no such count.
	Sent      *uint64
	Completed *uint64
	Errors    *uint64
	Timeouts  *uint64
	// QueueDepth is the most commands the kernel will have out with the device at once, and Timeout how many seconds it waits for one to come back
	// before giving up on it. Each is nil where the kernel does not say.
	QueueDepth *uint64
	Timeout    *uint64
}

// Enclosure is one shelf of slots, as its own processor describes it.
type Enclosure struct {
	// Name is the kernel's name for the enclosure today, 1:0:24:0: the value of the enclosure label.
	Name string
	// ID is the enclosure's own identifier, which stays the same when its name changes.
	ID       string
	Vendor   string
	Product  string
	Revision string
	// Expander is the expander the enclosure speaks through, and empty for one on the card itself.
	Expander string
	// VendorSpecific is what the maker put in the part of the enclosure's description left to it: a Supermicro backplane's firmware version,
	// x40-66.16.11.0.
	VendorSpecific string
	Slots          []Slot
}

// Slot is one bay of an enclosure.
type Slot struct {
	// Number is the slot's number as the enclosure gives it.
	Number string
	// Status is what the enclosure says of the slot, in the page's words: ok, critical, not_installed.
	Status string
	// Fault says the slot's fault light is asked for or the enclosure senses a fault there, and Locate that its locate light is on.
	Fault  bool
	Locate bool
	// Powered says the enclosure has power to the slot, and is nil for an enclosure that cannot say.
	Powered *bool
}

// Sensors is what a card and its enclosures measure of themselves.
type Sensors struct {
	// Temperatures is the card's own, one for each place on it one is taken. Empty for a card that has none, or that was not asked.
	Temperatures []Temperature
	// Speed is how fast the card's processor runs as a share of its full speed, and 0 for a card that does not say. PowerMode is the power it runs
	// on, full, reduced or standby, and empty for a card that does not say.
	Speed      float64
	PowerMode  string
	Enclosures []EnclosureSensors
	// Unread is what would not answer this time, and nil when everything asked did. The rest is as it was read.
	Unread error
	// Unavailable says what could not be asked at all here and why, as a line for the log: no leave to open a device, say. Empty when everything
	// could be asked.
	Unavailable string
}

// Nothing says there is not one reading here: nothing could be asked, or there was nothing to ask.
func (s *Sensors) Nothing() bool {
	return len(s.Temperatures) == 0 && len(s.Enclosures) == 0 && s.Unread == nil
}

// Temperature is one temperature a card takes of itself.
type Temperature struct {
	// Sensor is where it is taken: chip for the card's processor, board for the card around it.
	Sensor  string
	Celsius float64
}

// The places a card takes its own temperature.
const (
	SensorChip  = "chip"
	SensorBoard = "board"
)

// EnclosureSensors is what one enclosure says of everything in it that is not a slot.
type EnclosureSensors struct {
	// Name is the kernel's name for the enclosure, as Enclosure has it.
	Name string
	// Conditions is what the enclosure says of itself as a whole, by how bad: each is set while something in it is in that condition.
	Conditions Conditions
	Elements   []Element
	// Lanes is which of its connectors each phy of its expanders is wired to, and empty for an enclosure that does not say.
	Lanes []Lane
}

// Lane is one phy of an expander, and the connector of the enclosure it is wired to.
type Lane struct {
	// Expander is the kernel's name for the expander, expander-1:0, as Phy has it.
	Expander string
	Phy      int
	// Connector is what the enclosure calls the connector, as Element has it: ConnectorA, or a slot's Drive Connector 05.
	Connector string
}

// Conditions is how an enclosure sums itself up.
type Conditions struct {
	Info          bool
	NonCritical   bool
	Critical      bool
	Unrecoverable bool
}

// Element is one thing an enclosure reports on: a fan, a power supply, a temperature sensor, a connector.
type Element struct {
	// Kind is what it is, in the page's words: cooling, power_supply, temperature_sensor.
	Kind string
	// Name is what the enclosure calls it, or its number among those of its kind when the enclosure gives it no name. No two of a kind in one
	// enclosure share one.
	Name string
	// Status is what the enclosure says of it, in the page's words: ok, critical, not_installed.
	Status string
	// Fault says the enclosure reports it as failed, and PredictedFailure that it expects it to.
	Fault            bool
	PredictedFailure bool
	// Locate says whether its locate light is on, and is nil for a kind of thing that has none.
	Locate *bool
	// What it measures, each nil for an element that is not of that kind or gives no reading: a fan's speed in turns a minute, a sensor's
	// temperature, voltage or current.
	RPM     *float64
	Celsius *float64
	Volts   *float64
	Amperes *float64
	// Limits is the limits the enclosure holds a sensor to, and nil for one it sets none for and for anything that is not a sensor.
	Limits *Limits
	// Problems is the particular ways a power supply can be in trouble, each said to be so or not. Empty for anything else.
	Problems []Problem
}

// Limits is the limits an enclosure holds one of its sensors to: past a warning it calls the sensor not quite right, and past a critical one,
// critical. A temperature sensor's are in degrees; a voltage or a current sensor's are a share of what it should read, which the enclosure does not
// say, with the low ones below 0. One it does not set is nil.
type Limits struct {
	HighCritical *float64
	HighWarning  *float64
	LowWarning   *float64
	LowCritical  *float64
}

// Problem is one particular trouble an element can be in, and whether it is.
type Problem struct {
	Name string
	Set  bool
}
