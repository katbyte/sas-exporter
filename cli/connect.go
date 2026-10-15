package cli

import (
	"fmt"
	"os"

	"github.com/katbyte/sas-exporter/lib/devices"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/sysfs"
)

// connect makes what asks the kernel about its SAS cards: a reader of the /sys it is told, which asks the hardware itself, through the device
// files it is told, for what it was told to. A /sys that is not there is said now, since a machine with no card looks the same from then on.
func connect(from source) (poll.Kernel, error) {
	info, err := os.Stat(from.sysfs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", from.sysfs)
	}

	var ask sysfs.Devices
	if from.enclosures {
		ask.Diagnostic = devices.Dir(from.dev).Diagnostic
	}
	if from.card {
		ask.CardPage = devices.Dir(from.dev).CardPage
	}

	return sysfs.New(from.sysfs, ask), nil
}
