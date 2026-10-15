// Package ses reads what an enclosure says of itself in the pages of its diagnostics that describe it: what it holds, how each thing in it is
// doing, what each is called, and the limits it holds its sensors to. An enclosure answers in the SCSI Enclosure Services format, which is laid
// out here as the standard and the sg_ses tool have it.
package ses

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The pages an enclosure is asked for.
const (
	// PageConfiguration says what kinds of thing the enclosure holds and how many of each.
	PageConfiguration byte = 0x01
	// PageStatus says how each one is doing, four bytes apiece, in the order the configuration lists them.
	PageStatus byte = 0x02
	// PageNames says what the enclosure calls each one.
	PageNames byte = 0x07
	// PageLimits says the limits the enclosure holds each one to, four bytes apiece in the same order. Not every enclosure has it.
	PageLimits byte = 0x05
	// PageAdditional says more of some of its things: for an expander, which connector each of its phys is wired to. Not every enclosure has it.
	PageAdditional byte = 0x0a
)

// Where things are in every page: what page it is, then how long, then a number that changes whenever what the enclosure holds does.
const (
	pageHeader       = 8
	generationOffset = 4
	// statusSize is the bytes of status for one thing, and typeHeaderSize those that describe one kind of thing.
	statusSize     = 4
	typeHeaderSize = 4
	// nameHeader is the bytes before each name, the last two of which say how long the name is.
	nameHeader = 4
)

// Kind is what kind of thing an element is, by the number the standard gives it.
type Kind byte

const (
	KindDeviceSlot            Kind = 0x01
	KindPowerSupply           Kind = 0x02
	KindCooling               Kind = 0x03
	KindTemperatureSensor     Kind = 0x04
	KindDoor                  Kind = 0x05
	KindAudibleAlarm          Kind = 0x06
	KindControllerElectronics Kind = 0x07
	KindEnclosure             Kind = 0x0e
	KindVoltageSensor         Kind = 0x12
	KindCurrentSensor         Kind = 0x13
	KindArrayDeviceSlot       Kind = 0x17
	KindSASExpander           Kind = 0x18
	KindSASConnector          Kind = 0x19
)

// kinds is each kind in the page's words.
var kinds = map[Kind]string{
	0x00:                      "unspecified",
	KindDeviceSlot:            "device_slot",
	KindPowerSupply:           "power_supply",
	KindCooling:               "cooling",
	KindTemperatureSensor:     "temperature_sensor",
	KindDoor:                  "door",
	KindAudibleAlarm:          "audible_alarm",
	KindControllerElectronics: "controller_electronics",
	0x08:                      "scc_controller_electronics",
	0x09:                      "nonvolatile_cache",
	0x0a:                      "invalid_operation_reason",
	0x0b:                      "uninterruptible_power_supply",
	0x0c:                      "display",
	0x0d:                      "key_pad",
	KindEnclosure:             "enclosure",
	0x0f:                      "scsi_port",
	0x10:                      "language",
	0x11:                      "communication_port",
	KindVoltageSensor:         "voltage_sensor",
	KindCurrentSensor:         "current_sensor",
	0x14:                      "scsi_target_port",
	0x15:                      "scsi_initiator_port",
	0x16:                      "simple_subenclosure",
	KindArrayDeviceSlot:       "array_device_slot",
	KindSASExpander:           "sas_expander",
	KindSASConnector:          "sas_connector",
}

// String is the kind in the page's words, and its number for one the standard has added since or a maker has made up.
func (k Kind) String() string {
	if word, known := kinds[k]; known {
		return word
	}

	return fmt.Sprintf("kind_%d", byte(k))
}

// Slot says the kind is a bay for a disk, which the kernel already reports on without the enclosure being asked.
func (k Kind) Slot() bool {
	return k == KindDeviceSlot || k == KindArrayDeviceSlot
}

// codes is what an element's status says of it, in the page's words, by the number in the low half of its first byte. The first eight are the
// kernel's own words for a slot.
var codes = [16]string{"unsupported", "ok", "critical", "non_critical", "unrecoverable", "not_installed", "unknown", "unavailable", "no_access_allowed"}

// The codes of an element that is there and working well enough to measure anything.
const (
	codeOK            = 1
	codeUnrecoverable = 4
)

// Enclosure is what an enclosure says of itself as a whole and of each thing in it.
type Enclosure struct {
	// Info, NonCritical, Critical and Unrecoverable are set while something in the enclosure is in that condition.
	Info          bool
	NonCritical   bool
	Critical      bool
	Unrecoverable bool
	Elements      []Element
}

// Element is one thing in an enclosure and the four bytes it gave for how it is doing.
type Element struct {
	Kind Kind
	// Number is its place among those of its kind, from 0, and Name what the enclosure calls it, which may be nothing.
	Number int
	Name   string
	Status [statusSize]byte
	// limits is the four limits the enclosure holds it to, as the enclosure gives them, and all nothing until Limit has read them
	limits [statusSize]byte
}

// Limit reads the page of limits the enclosure holds its sensors to, which is laid out as the page of statuses is, and keeps each with the thing
// it is for. The page has to be of the enclosure as Parse read it. A real enclosure may end the page before its last things, which then have no
// limits.
func (e *Enclosure) Limit(configuration, limits []byte) error {
	listed, err := configured(configuration)
	if err != nil {
		return err
	}
	if err := page(limits, PageLimits, configuration); err != nil {
		return err
	}

	at, next := pageHeader, 0
	for _, k := range listed {
		// each kind starts with limits for all of that kind together, which are not used
		at += statusSize

		for range k.count {
			if at+statusSize > len(limits) || next >= len(e.Elements) {
				return nil
			}

			copy(e.Elements[next].limits[:], limits[at:])
			at += statusSize
			next++
		}
	}

	return nil
}

// Limits is the limits an enclosure holds one of its sensors to: past a warning it calls the sensor not quite right, and past a critical one,
// critical. A temperature sensor's are in degrees. A voltage or a current sensor's are a share of what it should read, which the enclosure does not
// say: 0.15 is 15% above it, and -0.15, for a low limit, 15% below. A limit the enclosure does not set is nil.
type Limits struct {
	HighCritical *float64
	HighWarning  *float64
	LowWarning   *float64
	LowCritical  *float64
}

// Limits is the limits a sensor is held to, and false for a sensor with none and for anything that is not a sensor. A current sensor has only
// high limits.
func (e *Element) Limits() (Limits, bool) {
	if e.limits == [statusSize]byte{} {
		return Limits{}, false
	}

	switch e.Kind {
	case KindTemperatureSensor:
		return Limits{HighCritical: degrees(e.limits[0]), HighWarning: degrees(e.limits[1]), LowWarning: degrees(e.limits[2]), LowCritical: degrees(e.limits[3])}, true
	case KindVoltageSensor:
		return Limits{HighCritical: share(e.limits[0], 1), HighWarning: share(e.limits[1], 1), LowWarning: share(e.limits[2], -1), LowCritical: share(e.limits[3], -1)}, true
	case KindCurrentSensor:
		return Limits{HighCritical: share(e.limits[0], 1), HighWarning: share(e.limits[1], 1)}, true
	default:
		return Limits{}, false
	}
}

// degrees is a temperature limit, given as a temperature is, and nil for 0, which is a limit not set.
func degrees(given byte) *float64 {
	if given == 0 {
		return nil
	}

	return new(float64(given) - celsiusOffset)
}

// share is a voltage or a current limit, given in steps of half a percent of what the sensor should read, above it or below it as direction says,
// and nil for 0, which is a limit not set.
func share(given byte, direction float64) *float64 {
	if given == 0 {
		return nil
	}

	return new(direction * float64(given) / 200)
}

// kind is how many of one kind of thing an enclosure holds.
type kind struct {
	kind  Kind
	count int
}

// Parse puts an enclosure's three pages together into what it holds and how each thing is doing. The three have to describe the same enclosure:
// one that changed between pages, a disk caddy pulled say, is an error, and worth asking again.
func Parse(configuration, status, names []byte) (Enclosure, error) {
	listed, err := configured(configuration)
	if err != nil {
		return Enclosure{}, err
	}

	if err := page(status, PageStatus, configuration); err != nil {
		return Enclosure{}, err
	}
	if err := page(names, PageNames, configuration); err != nil {
		return Enclosure{}, err
	}

	e := Enclosure{Info: status[1]&0x08 != 0, NonCritical: status[1]&0x04 != 0, Critical: status[1]&0x02 != 0, Unrecoverable: status[1]&0x01 != 0}

	seen := map[Kind]int{}
	at, called := pageHeader, pageHeader
	for _, k := range listed {
		// each kind starts with one status, and one name, for all of that kind together
		at += statusSize
		called = skipName(names, called)

		for range k.count {
			if at+statusSize > len(status) {
				return Enclosure{}, fmt.Errorf("the enclosure's status page ends after %d bytes, before the status of every %s", len(status), k.kind)
			}

			element := Element{Kind: k.kind, Number: seen[k.kind]}
			copy(element.Status[:], status[at:])
			element.Name, called = name(names, called)

			seen[k.kind]++
			at += statusSize
			e.Elements = append(e.Elements, element)
		}
	}

	return e, nil
}

// page checks one page is the one asked for, long enough to have its header, and of the same enclosure the configuration describes.
func page(got []byte, want byte, configuration []byte) error {
	if len(got) < pageHeader {
		return fmt.Errorf("the enclosure's page %#02x is %d bytes, too short to be one", want, len(got))
	}
	if got[0] != want {
		return fmt.Errorf("the enclosure answered with page %#02x when asked for page %#02x", got[0], want)
	}
	if !bytes.Equal(got[generationOffset:pageHeader], configuration[generationOffset:pageHeader]) {
		return errors.New("what the enclosure holds changed while it was being asked")
	}

	return nil
}

// configured reads the configuration page: a description of the enclosure, and of any others it speaks for, and then one short header for each kind
// of thing they hold between them.
func configured(configuration []byte) ([]kind, error) {
	if len(configuration) < pageHeader {
		return nil, fmt.Errorf("the enclosure's configuration page is %d bytes, too short to be one", len(configuration))
	}
	if configuration[0] != PageConfiguration {
		return nil, fmt.Errorf("the enclosure answered with page %#02x when asked for its configuration", configuration[0])
	}

	short := fmt.Errorf("the enclosure's configuration page ends after %d bytes, before what it says it holds", len(configuration))

	// the second byte counts the other enclosures this one speaks for; each has a description that says how long it is and how many kinds follow
	at, headers := pageHeader, 0
	for range int(configuration[1]) + 1 {
		if at+4 > len(configuration) {
			return nil, short
		}

		headers += int(configuration[at+2])
		at += int(configuration[at+3]) + 4
	}

	listed := make([]kind, 0, headers)
	for range headers {
		if at+typeHeaderSize > len(configuration) {
			return nil, short
		}

		listed = append(listed, kind{kind: Kind(configuration[at]), count: int(configuration[at+1])})
		at += typeHeaderSize
	}

	return listed, nil
}

// name reads the name at one place in the names page and gives back where the next is. A page that ends early leaves the rest unnamed.
func name(names []byte, at int) (text string, next int) {
	if at+nameHeader > len(names) {
		return "", len(names)
	}

	end := at + nameHeader + int(binary.BigEndian.Uint16(names[at+2:]))
	if end > len(names) {
		return "", len(names)
	}

	return strings.Trim(strings.ToValidUTF8(string(names[at+nameHeader:end]), ""), " \t\r\n\x00"), end
}

// skipName is where the name after the one at a place is.
func skipName(names []byte, at int) int {
	_, next := name(names, at)

	return next
}

// StatusWord is what the enclosure says of the element, in the page's words: ok, critical, not_installed.
func (e *Element) StatusWord() string {
	if word := codes[e.Status[0]&0x0f]; word != "" {
		return word
	}

	return "reserved"
}

// measuring says the element is there and in a state to measure anything: one that is not installed gives a speed or a temperature of 0, which
// is not one.
func (e *Element) measuring() bool {
	code := e.Status[0] & 0x0f

	return code >= codeOK && code <= codeUnrecoverable
}

// Fault says the enclosure reports the element as failed. Each kind keeps that in a place of its own.
func (e *Element) Fault() bool {
	switch e.Kind {
	case KindPowerSupply, KindCooling, KindSASConnector:
		return e.Status[3]&0x40 != 0
	case KindTemperatureSensor, KindVoltageSensor, KindCurrentSensor, KindControllerElectronics, KindSASExpander, KindAudibleAlarm, KindDoor:
		return e.Status[1]&0x40 != 0
	case KindEnclosure:
		return e.Status[2]&0x02 != 0
	default:
		return false
	}
}

// PredictedFailure says the enclosure expects the element to fail, which any kind of element can say.
func (e *Element) PredictedFailure() bool {
	return e.Status[0]&0x40 != 0
}

// Locate says whether the element's locate light is on, and false for a kind that has none: every kind reported beside the slots keeps it in the
// same place.
func (e *Element) Locate() (on, has bool) {
	switch e.Kind {
	case KindPowerSupply, KindCooling, KindTemperatureSensor, KindVoltageSensor, KindCurrentSensor, KindSASExpander, KindSASConnector, KindEnclosure:
		return e.Status[1]&0x80 != 0, true
	default:
		return false, false
	}
}

// RPM is a fan's speed in turns a minute, and false for anything that is not a fan giving one. The enclosure gives eleven bits and which of three
// scales they are on.
func (e *Element) RPM() (float64, bool) {
	if e.Kind != KindCooling || !e.measuring() {
		return 0, false
	}

	speed := float64(int(e.Status[1]&0x07)<<8 | int(e.Status[2]))
	switch (e.Status[1] >> 3) & 0x03 {
	case 0:
		return speed * 10, true
	case 1:
		return speed*10 + 20480, true
	case 2:
		return speed * 100, true
	}

	return 0, false
}

// celsiusOffset is what the enclosure adds to a temperature so that it can give one below freezing in a byte that cannot go below 0.
const celsiusOffset = 20

// Celsius is a sensor's temperature, and false for anything that is not a temperature sensor giving one.
func (e *Element) Celsius() (float64, bool) {
	if e.Kind != KindTemperatureSensor || !e.measuring() || e.Status[2] == 0 {
		return 0, false
	}

	return float64(e.Status[2]) - celsiusOffset, true
}

// hundredths is the last two bytes of a status as a number given in hundredths: the enclosure gives volts and amps in tens of thousandths.
func (e *Element) hundredths() float64 {
	return float64(int16(binary.BigEndian.Uint16(e.Status[2:]))) / 100 //nolint:gosec // G115: the two bytes are a signed number, which is what the conversion says
}

// Volts is a sensor's voltage, and false for anything that is not a voltage sensor giving one.
func (e *Element) Volts() (float64, bool) {
	if e.Kind != KindVoltageSensor || !e.measuring() {
		return 0, false
	}

	return e.hundredths(), true
}

// Amperes is a sensor's current, and false for anything that is not a current sensor giving one.
func (e *Element) Amperes() (float64, bool) {
	if e.Kind != KindCurrentSensor || !e.measuring() {
		return 0, false
	}

	return e.hundredths(), true
}

// Problem is one particular trouble a power supply can be in, and whether it is.
type Problem struct {
	Name string
	Set  bool
}

// Problems is the particular ways a power supply can be in trouble, each said to be so or not, and nothing for anything that is not one or is
// not there.
func (e *Element) Problems() []Problem {
	if e.Kind != KindPowerSupply || !e.measuring() {
		return nil
	}

	return []Problem{
		{"ac_fail", e.Status[3]&0x02 != 0},
		{"dc_fail", e.Status[3]&0x01 != 0},
		{"dc_overvoltage", e.Status[2]&0x08 != 0},
		{"dc_undervoltage", e.Status[2]&0x04 != 0},
		{"dc_overcurrent", e.Status[2]&0x02 != 0},
		{"overtemperature_fail", e.Status[3]&0x08 != 0},
		{"temperature_warning", e.Status[3]&0x04 != 0},
		{"off", e.Status[3]&0x10 != 0},
	}
}
