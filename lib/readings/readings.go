// Package readings is what the poller knows of each SAS card, as Prometheus readings for a metrics page to hold.
package readings

import (
	"strconv"

	"github.com/katbyte/go-kt/pointer"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/sas-exporter/lib/mpt"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/prometheus/client_golang/prometheus"
)

// Namespace starts every reading's name.
const Namespace = "sas"

// cardLabel says which card a reading hangs off, by what the card goes by: its model, or the name chosen for it.
const cardLabel = "card"

// The labels that say where on a card a thing is, and which disk it is.
const (
	expanderLabel  = "expander"
	phyLabel       = "phy"
	enclosureLabel = "enclosure"
	slotLabel      = "slot"
	serialLabel    = "serial"
	diskLabel      = "disk"
	typeLabel      = "type"
	elementLabel   = "element"
)

// The words for how an enclosure sums itself up, as the condition label has them.
const (
	conditionInfo          = "info"
	conditionNonCritical   = "non_critical"
	conditionCritical      = "critical"
	conditionUnrecoverable = "unrecoverable"
)

// The limits an enclosure holds a sensor to, as the limit label names them.
const (
	limitHighCritical = "high_critical"
	limitHighWarning  = "high_warning"
	limitLowWarning   = "low_warning"
	limitLowCritical  = "low_critical"
)

// The kinds of sensor an enclosure holds to limits, as the type label names them.
const (
	temperatureSensor = "temperature_sensor"
	voltageSensor     = "voltage_sensor"
	currentSensor     = "current_sensor"
)

// The kinds of error a phy counts, as the error label names them.
const (
	invalidDword     = "invalid_dword"
	runningDisparity = "running_disparity"
	lossOfDwordSync  = "loss_of_dword_sync"
	phyResetProblem  = "phy_reset_problem"
)

// list is every reading the page can hold, each with the card's label first.
var list = metrics.NewList(Namespace, cardLabel)

// reading describes one reading of a card; labels are the ones it has beside the card's.
func reading(name, help string, labels ...string) *prometheus.Desc {
	return list.Reading(name, help, labels...)
}

var (
	up               = reading("card_up", "Whether the kernel had the card and had it in service the last time it was asked (1) or not (0).")
	cardInfo         = reading("card_info", "Always 1, with the card's model, the versions of its firmware, its BIOS and its driver, its SAS address, and the kernel's name for it today, which is the number its expanders and enclosures are named with. Then its serial number, part number and chip, the versions of the settings it keeps (nvdata) and came with (nvdata_default), and of the language it and its driver speak (mpi).", "model", "firmware", "bios", "driver", "driver_version", "sas_address", "scsi_host", serialLabel, "assembly", "chip", "nvdata", "nvdata_default", "mpi")
	cardResets       = reading("card_resets_total", "How many times the card's firmware has been restarted since its driver loaded.")
	cardInFlight     = reading("card_commands_in_flight", "Commands the kernel had sent the card that had not come back, at the moment the card was last asked: a glimpse once a links interval, so it shows a card that stays busy and not a burst.")
	cardQueue        = reading("card_commands_in_flight_limit", "The most commands the kernel will have out with the card at once: in flight near this for long is a card given more than it can take.")
	cardTemperature  = reading("card_temperature_celsius", "The card's own temperature, by where on the card it is taken: chip for its processor, board for the card around it.", "sensor")
	cardSpeed        = reading("card_speed_ratio", "How fast the card's processor runs, as a share of its full speed: below 1 is a card that has slowed itself, to keep cool or to save power.")
	cardPowerMode    = reading("card_power_mode", "Always 1 for the power mode the card says it is in, full, reduced or standby, and 0 for the others.", "mode")
	cardSlotSpeed    = reading("card_pcie_link_speed_gigatransfers_per_second", "The speed of each lane of the card's own link to the machine, in gigatransfers a second: 16 is PCIe 4.0, 8 is 3.0.")
	cardSlotMaxSpeed = reading("card_pcie_maximum_link_speed_gigatransfers_per_second", "The fastest each lane of the card's own link to the machine could be: a link slower than this is a card in a slower slot than it was made for.")
	cardSlotWidth    = reading("card_pcie_link_width", "How many lanes the card's own link to the machine came up with.")
	cardSlotMaxWidth = reading("card_pcie_maximum_link_width", "How many lanes the card's own link to the machine could have: a link narrower than this is a card in a narrower slot, or not seated.")

	expanderInfo = reading("expander_info", "Always 1, with the expander's maker, product and revision, its SAS address, which stays the same when the kernel's name for the expander changes, and the chip it is built on by its maker, number and revision.", expanderLabel, "vendor", "product", "revision", "sas_address", "chip_vendor", "chip", "chip_revision")

	phyInfo      = reading("phy_info", "Always 1, with what is on the other end of the phy's link: device for a disk or an enclosure's own processor, expander for a further expander, upstream for the way back to the card, none for nothing the kernel lists.", expanderLabel, phyLabel, "attached")
	phyLinkState = reading("phy_link_state", "Always 1, with what the phy says of its link: up, down, disabled, failed when the two ends could not agree a speed, spinup_hold for a disk waiting to be told to spin up, or other.", expanderLabel, phyLabel, "state")
	phyRate      = reading("phy_link_rate_gigabits_per_second", "The speed the phy's link came up at, in gigabits a second: 12, 6, 3 or 1.5 for SAS, and 0 with no link.", expanderLabel, phyLabel)
	phyMaximum   = reading("phy_maximum_link_rate_gigabits_per_second", "The most the phy is set to offer, in gigabits a second: a link slower than both its ends offer has been held back by something between them.", expanderLabel, phyLabel)
	phyMinimum   = reading("phy_minimum_link_rate_gigabits_per_second", "The least the phy is set to offer, in gigabits a second: a link that cannot come up at this or faster does not come up at all.", expanderLabel, phyLabel)
	phyHWMaximum = reading("phy_hardware_maximum_link_rate_gigabits_per_second", "The most the phy can do, in gigabits a second: one set to offer less than this has been held back on purpose.", expanderLabel, phyLabel)
	phyHWMinimum = reading("phy_hardware_minimum_link_rate_gigabits_per_second", "The least the phy can do, in gigabits a second.", expanderLabel, phyLabel)
	phyConnector = reading("phy_connector_info", "Always 1, with the connector the phy is wired to, as the enclosure the expander is in names it: ConnectorA for a cable's, Drive Connector 05 for a slot's.", expanderLabel, phyLabel, enclosureLabel, "connector")
	phyErrors    = reading("phy_errors_total", "Errors the phy has counted on its link since it was last reset, by kind: invalid_dword, running_disparity, loss_of_dword_sync and phy_reset_problem. One that keeps growing is a cable, a connector or a slot going bad.", expanderLabel, phyLabel, "error")

	deviceInfo      = reading("device_info", "Always 1, with what the device is and where: its world wide name, maker, model and firmware, its SAS address, the phy its link is on, and the enclosure and slot it sits in.", serialLabel, diskLabel, "wwn", "vendor", "model", "revision", "sas_address", expanderLabel, phyLabel, enclosureLabel, slotLabel)
	deviceState     = reading("device_state", "Always 1, with what the kernel says the device is doing: running, or blocked, offline, transport_offline and the like for one it has stopped sending to.", serialLabel, diskLabel, "state")
	deviceSent      = reading("device_commands_total", "Commands the kernel has sent the device since it appeared.", serialLabel, diskLabel)
	deviceCompleted = reading("device_commands_completed_total", "Commands the device has answered since it appeared, well or badly: fewer than were sent, for long, is a command stuck with the device.", serialLabel, diskLabel)
	deviceErrors    = reading("device_command_errors_total", "Commands the device answered with anything but success since it appeared. A question it merely declines counts as well as a fault, so watch for one disk growing faster than its neighbours.", serialLabel, diskLabel)
	deviceTimeouts  = reading("device_command_timeouts_total", "Commands the device never answered since it appeared.", serialLabel, diskLabel)
	deviceQueue     = reading("device_queue_depth", "The most commands the kernel will have out with the device at once: sent less completed near this for long is a disk given more than it can take.", serialLabel, diskLabel)
	deviceTimeout   = reading("device_timeout_seconds", "How long the kernel waits for the device to answer a command before giving up on it and trying to get it back.", serialLabel, diskLabel)

	enclosureInfo  = reading("enclosure_info", "Always 1, with the enclosure's maker, product and revision, the expander it speaks through, its own identifier, which stays the same when the kernel's name for the enclosure changes, and what its maker put in the part of its description left to it: a Supermicro backplane's firmware version.", enclosureLabel, "vendor", "product", "revision", "id", expanderLabel, "vendor_specific")
	enclosureSlots = reading("enclosure_slots", "How many slots the enclosure has.", enclosureLabel)
	slotStatus     = reading("enclosure_slot_status", "Always 1, with what the enclosure says of the slot: ok, not_installed for an empty one, or critical, non_critical, unrecoverable, unavailable, unknown and unsupported.", enclosureLabel, slotLabel, "status")
	slotFault      = reading("enclosure_slot_fault", "Whether the slot's fault light is asked for or the enclosure senses a fault there (1) or not (0).", enclosureLabel, slotLabel)
	slotLocate     = reading("enclosure_slot_locate", "Whether the slot's locate light is on (1) or not (0).", enclosureLabel, slotLabel)
	slotPowered    = reading("enclosure_slot_powered", "Whether the enclosure has power to the slot (1) or has turned it off (0).", enclosureLabel, slotLabel)

	enclosureCondition = reading("enclosure_condition", "Whether the enclosure says something in it is in that condition (1) or not (0): info, non_critical, critical or unrecoverable.", enclosureLabel, "condition")
	elementStatus      = reading("enclosure_element_status", "Always 1, with what the enclosure says of one of the things in it that is not a slot: a fan, a power supply, a sensor, a connector. ok, not_installed, critical and the rest are as for a slot.", enclosureLabel, typeLabel, elementLabel, "status")
	elementFault       = reading("enclosure_element_fault", "Whether the enclosure reports the thing as failed (1) or not (0).", enclosureLabel, typeLabel, elementLabel)
	elementPredicted   = reading("enclosure_element_predicted_failure", "Whether the enclosure expects the thing to fail (1) or not (0).", enclosureLabel, typeLabel, elementLabel)
	elementLocate      = reading("enclosure_element_locate", "Whether the thing's locate light is on (1) or not (0), for the kinds of thing that have one.", enclosureLabel, typeLabel, elementLabel)
	fanSpeed           = reading("enclosure_fan_speed_rpm", "How fast a fan the enclosure watches is turning, in turns a minute.", enclosureLabel, elementLabel)
	temperature        = reading("enclosure_temperature_celsius", "What one of the enclosure's temperature sensors reads.", enclosureLabel, elementLabel)
	temperatureLimit   = reading("enclosure_temperature_limit_celsius", "A limit the enclosure holds one of its temperature sensors to, by which: high_critical, high_warning, low_warning or low_critical.", enclosureLabel, elementLabel, "limit")
	voltage            = reading("enclosure_voltage_volts", "What one of the enclosure's voltage sensors reads.", enclosureLabel, elementLabel)
	voltageLimit       = reading("enclosure_voltage_limit_ratio", "A limit the enclosure holds one of its voltage sensors to, as a share of what the sensor should read, which the enclosure does not say: 0.15 is 15% above it and -0.15 15% below. By which: high_critical, high_warning, low_warning or low_critical.", enclosureLabel, elementLabel, "limit")
	current            = reading("enclosure_current_amperes", "What one of the enclosure's current sensors reads.", enclosureLabel, elementLabel)
	currentLimit       = reading("enclosure_current_limit_ratio", "A limit the enclosure holds one of its current sensors to, as a share above what the sensor should read, which the enclosure does not say: 0.15 is 15% above it. By which: high_critical or high_warning.", enclosureLabel, elementLabel, "limit")
	supplyProblem      = reading("enclosure_power_supply_problem", "Whether a power supply is in that particular trouble (1) or not (0): ac_fail, dc_fail, dc_overvoltage, dc_undervoltage, dc_overcurrent, overtemperature_fail, temperature_warning or off.", enclosureLabel, elementLabel, "problem")
)

// Source is where the cards' state comes from: the poller, or a test's own.
type Source interface {
	State() poll.State
}

// Readings writes what a source knows as readings. It asks the kernel nothing, so a scrape is answered from memory however slow a card is.
type Readings struct {
	source Source
}

// New is the readings of one machine's cards.
func New(source Source) *Readings {
	return &Readings{source: source}
}

// Describe names every reading there can be.
func (*Readings) Describe(ch chan<- *prometheus.Desc) {
	list.Describe(ch)
}

// Collect writes every reading there is an answer for. A card that has never answered a question has no reading of it, which is not the same as a
// reading of zero.
func (r *Readings) Collect(ch chan<- prometheus.Metric) {
	s := r.source.State()
	for i := range s.Cards {
		card := &s.Cards[i]
		w := writer{metrics.NewWriter(ch, card.Called)}

		w.inService(card)
		w.card(card)
		w.links(card)
		w.enclosures(card)
		w.sensors(card)
	}
}

// writer writes one card's readings, each with the card's name on it. A thing written twice is written once: a kernel that listed the same phy
// twice would otherwise put two readings under one set of labels, and spoil the whole page.
type writer struct {
	*metrics.Writer
}

// inService is whether the kernel has the card in service, once it has been asked. How the asking itself is going is go-kt's poller's to write.
func (w writer) inService(card *poll.Card) {
	if card.LinksRound.Ran {
		w.Flag(up, card.Up())
	}
}

// card is what the card says of itself.
func (w writer) card(card *poll.Card) {
	if !card.HBA.Known() {
		return
	}

	hba := card.HBA.Value
	w.Gauge(cardInfo, 1, hba.Model, hba.Firmware, hba.BIOS, hba.Driver, hba.DriverVersion, hba.SASAddress, card.Name, hba.Serial, hba.Assembly, hba.Chip, hba.NVData, hba.NVDataDefault, hba.MPI)
	if hba.Resets != nil {
		w.Total(cardResets, float64(pointer.From(hba.Resets)))
	}
	if hba.InFlight != nil {
		w.Gauge(cardInFlight, pointer.From(hba.InFlight))
	}
	if hba.Queue != nil {
		w.Gauge(cardQueue, pointer.From(hba.Queue))
	}

	if hba.Slot == nil {
		return
	}

	// a speed or a width the kernel does not know is left off: 0 would say the link is down
	slot := pointer.From(hba.Slot)
	for d, value := range map[*prometheus.Desc]float64{cardSlotSpeed: slot.Speed, cardSlotMaxSpeed: slot.MaximumSpeed, cardSlotWidth: float64(slot.Width), cardSlotMaxWidth: float64(slot.MaximumWidth)} {
		if value > 0 {
			w.Gauge(d, value)
		}
	}
}

// links is the expanders, the phys and the devices that hang off the card.
func (w writer) links(card *poll.Card) {
	if !card.Links.Known() {
		return
	}

	links := card.Links.Value
	for _, e := range links.Expanders {
		if w.First(expanderLabel, e.Name) {
			w.Gauge(expanderInfo, 1, e.Name, e.Vendor, e.Product, e.Revision, e.SASAddress, e.ChipVendor, e.Chip, e.ChipRevision)
		}
	}

	for i := range links.Phys {
		w.phy(&links.Phys[i])
	}

	for i := range links.Devices {
		w.device(&links.Devices[i])
	}
}

// phy is one phy's link, and its error counts when it gave them. A phy with no link has a speed of 0, which is what it is running at.
func (w writer) phy(phy *poll.Phy) {
	number := strconv.Itoa(phy.Number)
	if !w.First(phyLabel, phy.Expander, number) {
		return
	}

	w.Gauge(phyInfo, 1, phy.Expander, number, string(phy.Attached))
	w.Gauge(phyLinkState, 1, phy.Expander, number, string(phy.Link))
	w.Gauge(phyRate, phy.Rate, phy.Expander, number)

	// a speed the phy does not say is left off: 0 would read as a phy that can do nothing
	for d, value := range map[*prometheus.Desc]float64{phyMaximum: phy.Maximum, phyMinimum: phy.Minimum, phyHWMaximum: phy.HardwareMaximum, phyHWMinimum: phy.HardwareMinimum} {
		if value > 0 {
			w.Gauge(d, value, phy.Expander, number)
		}
	}

	if phy.Errors == nil {
		return
	}

	errors := pointer.From(phy.Errors)
	w.Total(phyErrors, float64(errors.InvalidDwords), phy.Expander, number, invalidDword)
	w.Total(phyErrors, float64(errors.RunningDisparity), phy.Expander, number, runningDisparity)
	w.Total(phyErrors, float64(errors.LossOfDwordSync), phy.Expander, number, lossOfDwordSync)
	w.Total(phyErrors, float64(errors.ResetProblems), phy.Expander, number, phyResetProblem)
}

// device is one device at the end of a line, under its serial number and the kernel's name for it.
func (w writer) device(device *poll.Device) {
	if !w.First(diskLabel, device.Serial, device.Disk) {
		return
	}

	// a device whose port has no phy the kernel lists has no phy to name
	phy := ""
	if device.Phy >= 0 {
		phy = strconv.Itoa(device.Phy)
	}

	w.Gauge(deviceInfo, 1, device.Serial, device.Disk, device.WWN, device.Vendor, device.Model, device.Revision, device.SASAddress, device.Expander, phy, device.Enclosure, device.Slot)
	if device.State != "" {
		w.Gauge(deviceState, 1, device.Serial, device.Disk, device.State)
	}
	if device.Sent != nil {
		w.Total(deviceSent, float64(pointer.From(device.Sent)), device.Serial, device.Disk)
	}
	if device.Completed != nil {
		w.Total(deviceCompleted, float64(pointer.From(device.Completed)), device.Serial, device.Disk)
	}
	if device.Errors != nil {
		w.Total(deviceErrors, float64(pointer.From(device.Errors)), device.Serial, device.Disk)
	}
	if device.Timeouts != nil {
		w.Total(deviceTimeouts, float64(pointer.From(device.Timeouts)), device.Serial, device.Disk)
	}
	if device.QueueDepth != nil {
		w.Gauge(deviceQueue, pointer.From(device.QueueDepth), device.Serial, device.Disk)
	}
	if device.Timeout != nil {
		w.Gauge(deviceTimeout, pointer.From(device.Timeout), device.Serial, device.Disk)
	}
}

// enclosures is each enclosure on the card and what it says of its slots.
func (w writer) enclosures(card *poll.Card) {
	if !card.Enclosures.Known() {
		return
	}

	for _, e := range card.Enclosures.Value {
		if !w.First(enclosureLabel, e.Name) {
			continue
		}

		w.Gauge(enclosureInfo, 1, e.Name, e.Vendor, e.Product, e.Revision, e.ID, e.Expander, e.VendorSpecific)
		w.Gauge(enclosureSlots, float64(len(e.Slots)), e.Name)

		for _, slot := range e.Slots {
			if !w.First(slotLabel, e.Name, slot.Number) {
				continue
			}

			w.Gauge(slotStatus, 1, e.Name, slot.Number, slot.Status)
			w.Flag(slotFault, slot.Fault, e.Name, slot.Number)
			w.Flag(slotLocate, slot.Locate, e.Name, slot.Number)
			if slot.Powered != nil {
				w.Flag(slotPowered, pointer.From(slot.Powered), e.Name, slot.Number)
			}
		}
	}
}

// sensors is what the card and its enclosures measure of themselves, where there was leave to ask.
func (w writer) sensors(card *poll.Card) {
	if !card.Sensors.Known() {
		return
	}

	sensors := card.Sensors.Value
	for _, t := range sensors.Temperatures {
		if w.First("temperature", t.Sensor) {
			w.Gauge(cardTemperature, t.Celsius, t.Sensor)
		}
	}
	if sensors.Speed > 0 {
		w.Gauge(cardSpeed, sensors.Speed)
	}
	if sensors.PowerMode != "" {
		w.States(cardPowerMode, mpt.PowerModes, sensors.PowerMode)
	}

	for i := range sensors.Enclosures {
		e := &sensors.Enclosures[i]
		if !w.First("sensors", e.Name) {
			continue
		}

		w.Flag(enclosureCondition, e.Conditions.Info, e.Name, conditionInfo)
		w.Flag(enclosureCondition, e.Conditions.NonCritical, e.Name, conditionNonCritical)
		w.Flag(enclosureCondition, e.Conditions.Critical, e.Name, conditionCritical)
		w.Flag(enclosureCondition, e.Conditions.Unrecoverable, e.Name, conditionUnrecoverable)

		for j := range e.Elements {
			w.element(e.Name, &e.Elements[j])
		}

		for _, lane := range e.Lanes {
			number := strconv.Itoa(lane.Phy)
			if w.First("lane", lane.Expander, number) {
				w.Gauge(phyConnector, 1, lane.Expander, number, e.Name, lane.Connector)
			}
		}
	}
}

// element is one thing in an enclosure that is not a slot: what the enclosure says of it, whether it has failed, and what it measures if it
// measures anything.
func (w writer) element(enclosure string, element *poll.Element) {
	if !w.First(elementLabel, enclosure, element.Kind, element.Name) {
		return
	}

	w.Gauge(elementStatus, 1, enclosure, element.Kind, element.Name, element.Status)
	w.Flag(elementFault, element.Fault, enclosure, element.Kind, element.Name)
	w.Flag(elementPredicted, element.PredictedFailure, enclosure, element.Kind, element.Name)
	if element.Locate != nil {
		w.Flag(elementLocate, pointer.From(element.Locate), enclosure, element.Kind, element.Name)
	}

	for d, value := range map[*prometheus.Desc]*float64{fanSpeed: element.RPM, temperature: element.Celsius, voltage: element.Volts, current: element.Amperes} {
		if value != nil {
			w.Gauge(d, pointer.From(value), enclosure, element.Name)
		}
	}

	for _, problem := range element.Problems {
		w.Flag(supplyProblem, problem.Set, enclosure, element.Name, problem.Name)
	}

	held := map[string]*prometheus.Desc{temperatureSensor: temperatureLimit, voltageSensor: voltageLimit, currentSensor: currentLimit}[element.Kind]
	if element.Limits == nil || held == nil {
		return
	}
	for limit, at := range map[string]*float64{limitHighCritical: element.Limits.HighCritical, limitHighWarning: element.Limits.HighWarning, limitLowWarning: element.Limits.LowWarning, limitLowCritical: element.Limits.LowCritical} {
		if at != nil {
			w.Gauge(held, pointer.From(at), enclosure, element.Name, limit)
		}
	}
}
