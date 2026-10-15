package ses

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Lane is one phy of an expander an enclosure describes, and the connector it is wired to.
type Lane struct {
	// Expander is the expander's SAS address, as sixteen small hex digits, which is how the kernel's expander is found.
	Expander string
	Phy      int
	// Connector is where among the enclosure's elements the connector is: one of its SAS connectors, ConnectorA or a slot's Drive Connector 05.
	Connector int
}

// The kinds of element the page of more about things has a description for, in order, when it does not say which element each is for.
var described = map[Kind]bool{KindDeviceSlot: true, KindArrayDeviceSlot: true, KindSASExpander: true, KindControllerElectronics: true, 0x14: true, 0x15: true}

// What the page of more about things says of each description.
const (
	// laneInvalid marks a description not to be used, elementIndexed one that says which element it is for, and protocolSAS one in SAS's terms
	laneInvalid    = 0x80
	elementIndexed = 0x10
	protocolMask   = 0x0f
	protocolSAS    = 0x06
	// expanderDescription is the kind of SAS description an expander has: its phys' count, its address, then two bytes for each phy
	expanderDescription = 1
	expanderAddress     = 4
	expanderPhys        = 12
	// noElement is an index that points at nothing: a phy wired to no connector
	noElement = 0xff
)

// Lanes reads the page an enclosure says more of its things in, and gives which connector each phy of each of its expanders is wired to. A phy
// wired to none is left out. The page has to be of the enclosure as Parse read it, and the configuration the one it was read with.
func (e *Enclosure) Lanes(configuration, additional []byte) ([]Lane, error) {
	listed, err := configured(configuration)
	if err != nil {
		return nil, err
	}
	if err := page(additional, PageAdditional, configuration); err != nil {
		return nil, err
	}

	idx := e.indexes(listed)
	end := min(len(additional), pageHeader-4+int(binary.BigEndian.Uint16(additional[2:])))

	var lanes []Lane
	next := 0 // for a page that does not say which element a description is for: the next element of the kinds described, in order
	for at := pageHeader; at+2 <= end; {
		flags, length := additional[at], int(additional[at+1])+2
		if at+length > end {
			return nil, fmt.Errorf("the enclosure's page %#02x ends inside a description", PageAdditional)
		}
		// a description that says which element it is for needs room to say so
		if flags&elementIndexed != 0 && length < 4 {
			at += length

			continue
		}

		body, scheme, element := at+2, byte(0), next
		if flags&elementIndexed != 0 {
			body, scheme = at+4, additional[at+2]&0x03
			element = idx.element(scheme, int(additional[at+3]))
		} else {
			for element < len(e.Elements) && !described[e.Elements[element].Kind] {
				element++
			}
			next = element + 1
		}

		if flags&laneInvalid == 0 && flags&protocolMask == protocolSAS && element >= 0 && element < len(e.Elements) && e.Elements[element].Kind == KindSASExpander && body+expanderPhys <= at+length && additional[body+1]>>6 == expanderDescription {
			address := hex.EncodeToString(additional[body+expanderAddress : body+expanderAddress+8])
			for phy := range int(additional[body]) {
				pair := body + expanderPhys + 2*phy
				if pair+2 > at+length {
					break
				}
				// an index that lands on anything but a connector is the enclosure counting some way not known here, and is left out
				if connector := idx.connector(scheme, int(additional[pair])); additional[pair] != noElement && connector >= 0 && e.Elements[connector].Kind == KindSASConnector {
					lanes = append(lanes, Lane{Expander: address, Phy: phy, Connector: connector})
				}
			}
		}

		at += length
	}

	return lanes, nil
}

// indexes is the ways an enclosure numbers its elements, each giving where among Elements one is: by every status in the page, the one for each
// kind as a whole too; by every element's own; and among the SAS connectors alone.
type indexes struct {
	all        []int
	connectors []int
	elements   int
}

func (e *Enclosure) indexes(listed []kind) indexes {
	idx := indexes{elements: len(e.Elements)}
	at := 0
	for _, k := range listed {
		idx.all = append(idx.all, -1)
		for range k.count {
			if at < len(e.Elements) && e.Elements[at].Kind == KindSASConnector {
				idx.connectors = append(idx.connectors, at)
			}
			idx.all = append(idx.all, at)
			at++
		}
	}

	return idx
}

// element is where among Elements the element a description is for is, by the way the page says it counts: with the statuses for each kind as a
// whole, or without. -1 for one that is no element.
func (idx indexes) element(scheme byte, index int) int {
	if scheme == 1 {
		return pick(idx.all, index)
	}
	if index < idx.elements {
		return index
	}

	return -1
}

// connector is where among Elements the connector a phy is wired to is. An enclosure that counts no other way counts its SAS connectors alone.
func (idx indexes) connector(scheme byte, index int) int {
	switch scheme {
	case 0:
		return pick(idx.connectors, index)
	case 2:
		return idx.element(scheme, index)
	default:
		return pick(idx.all, index)
	}
}

// pick is the place at index in places, and -1 for an index past the end.
func pick(places []int, index int) int {
	if index < 0 || index >= len(places) {
		return -1
	}

	return places[index]
}
