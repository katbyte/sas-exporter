// Package skip is the parts of what sas-exporter reports that can be left out, by the names the settings give them. A part left out is not asked
// for either, so leaving out one that costs a card or an enclosure a question saves the question too.
package skip

import (
	"fmt"
	"slices"
	"strings"
)

// Part is one part of what is reported, by the name the settings give it.
type Part string

// The parts, each a family of readings or the questions behind some of them.
const (
	// Poll is how the asking goes: how often each round runs, how long it took, when it last read everything and how often it failed. Nothing is
	// asked for it, so leaving it out saves only the page.
	Poll Part = "poll"
	// Cards is what the kernel says of each card: its model, versions, restarts, own link and how busy it is. Whether it is up is always reported.
	Cards Part = "cards"
	// Expanders is what each expander is, and how often anything about it has changed.
	Expanders Part = "expanders"
	// Phys is everything about each lane: its link, its speeds, its errors, how often it has changed and which connector it is wired to.
	Phys Part = "phys"
	// PhyErrors is a lane's error counts alone, each of which the kernel fetches from the card or an expander as it is read.
	PhyErrors Part = "phy-errors"
	// LinkChanges is how often each lane's link and each expander has changed, which takes about a hundred questions of each card a round.
	LinkChanges Part = "link-changes"
	// Devices is each disk.
	Devices Part = "devices"
	// Enclosures is everything about each enclosure: what it is, its slots, and what it measures of itself.
	Enclosures Part = "enclosures"
	// Slots is each enclosure's slots, each of which has the kernel fetch the enclosure's whole status page as it is read.
	Slots Part = "slots"
	// CardSensors is everything asked of a card itself, which takes root: its temperature, speed, power, connectors and link changes.
	CardSensors Part = "card-sensors"
	// EnclosureSensors is everything asked of an enclosure itself, which takes root: its fans, power supplies, sensors, limits and lanes.
	EnclosureSensors Part = "enclosure-sensors"
)

// Parts is every part, in the order they are listed.
var Parts = []Part{Poll, Cards, Expanders, Phys, PhyErrors, LinkChanges, Devices, Enclosures, Slots, CardSensors, EnclosureSensors}

// within is the parts each part includes, which leaving it out leaves out too.
var within = map[Part][]Part{
	Phys:        {PhyErrors, LinkChanges},
	Enclosures:  {Slots, EnclosureSensors},
	CardSensors: {LinkChanges},
}

// Set is the parts left out. The empty set leaves nothing out.
type Set map[Part]bool

// Parse reads a list of parts to leave out, with a comma between: phy-errors,slots. A name that is no part is refused rather than left to leave
// nothing out unnoticed.
func Parse(list string) (Set, error) {
	s := Set{}
	for named := range strings.SplitSeq(list, ",") {
		part := Part(strings.ToLower(strings.TrimSpace(named)))
		if part == "" {
			continue
		}
		if !slices.Contains(Parts, part) {
			return nil, fmt.Errorf("%q is not one of the parts: %s", named, s.list(Parts))
		}

		s[part] = true
	}

	return s, nil
}

// AllBut reads a list of the only parts to ask about, as --metrics gives it, into the parts that leaves out: every other, keeping what is within
// those named and what they are within, since a lane's errors cannot be read without the lane. Nothing named leaves nothing out.
func AllBut(list string) (Set, error) {
	named, err := Parse(list)
	if err != nil || len(named) == 0 {
		return named, err
	}

	s := Set{}
	for _, part := range Parts {
		if !named.Has(part) && !named.within(part) {
			s[part] = true
		}
	}

	return s, nil
}

// within says whether a part named is within this one.
func (s Set) within(part Part) bool {
	return slices.ContainsFunc(within[part], func(inner Part) bool { return s[inner] })
}

// Has says whether a part is left out: named, or within one that is.
func (s Set) Has(part Part) bool {
	if s[part] {
		return true
	}

	for outer, inner := range within {
		if s[outer] && slices.Contains(inner, part) {
			return true
		}
	}

	return false
}

// String is the parts left out, as a list the settings would take.
func (s Set) String() string {
	var named []Part
	for _, part := range Parts {
		if s[part] {
			named = append(named, part)
		}
	}

	return s.list(named)
}

func (Set) list(parts []Part) string {
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		names = append(names, string(part))
	}

	return strings.Join(names, ", ")
}
