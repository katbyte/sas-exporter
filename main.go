// Package main implements sas-exporter, which reports the state of a machine's SAS cards, expanders, enclosures and disks as a Prometheus metrics
// page.
package main

import (
	"os"

	"github.com/gookit/color"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/sas-exporter/cli"
)

func main() {
	// the log level comes from SAS_EXPORTER_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("SAS_EXPORTER_LOG")

	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(color.Sprintf("<red>sas-exporter: building cmd</> %v", err))

		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(color.Sprintf("<red>sas-exporter:</> %v", err))

		os.Exit(1)
	}

	os.Exit(0)
}
