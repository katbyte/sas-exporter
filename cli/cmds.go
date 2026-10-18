// Package cli implements the sas-exporter command line interface: the cobra commands, flag and config handling, and the server that serves the
// metrics page.
package cli

import (
	"fmt"
	"strings"

	"github.com/katbyte/go-kt/prometheus/exporter"
	"github.com/katbyte/go-kt/version"
	"github.com/spf13/cobra"
)

func Make() (*cobra.Command, error) {
	return makeWith(connect)
}

// makeWith is Make with what asks the kernel made by connect, so a test can stand in for one.
func makeWith(connect connector) (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "sas-exporter [command]",
		Short: "sas-exporter reports the state of a machine's SAS cards, expanders, enclosures and disks as a Prometheus metrics page",
		Long: `Reports what the Linux kernel knows of the SAS cards in the machine it runs on as a Prometheus
metrics page: each card, the expanders behind it, every phy with its link speed and error
counts, the disks, and each enclosure's slots. Run as root, it also asks the enclosures for
their fans, power supplies and temperatures and the cards for their own. It only ever reads.
Complete documentation is available at https://github.com/katbyte/sas-exporter`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run \"sas-exporter help\" for more information about available sas-exporter commands.")
			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "Print the version number of sas-exporter",
		Long:          `Print the version number of sas-exporter`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Ask the kernel once and print the metrics page",
		Long: `Asks the kernel every question once and prints the page a scrape would read, without
serving anything: a check that the cards can be seen from where the program runs, and a
look at what would be reported.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
			poller, err := f.poller(connect)
			if err != nil {
				return err
			}

			p, err := f.page(poller)
			if err != nil {
				return err
			}

			// the page is printed either way, with what a card that did not answer looks like on it
			if err := exporter.Print(cmd.Context(), cmd.OutOrStdout(), poller.Inner(), p); err != nil {
				return err
			}
			if len(poller.Inner().Targets()) == 0 {
				return fmt.Errorf("the kernel lists no SAS card under %s: this has to run on the machine the cards are in, with --sysfs (%s) naming its /sys", f.Sysfs, envNames["sysfs"])
			}

			return nil
		},
	})

	root.AddCommand(serveCmd(connect))

	if err := configureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}
	// the settings file is read once the flags are parsed, since a flag can name it; before any command's own checks, which read what it holds
	root.PersistentPreRunE = func(*cobra.Command, []string) error { return loadConfig() }

	return root, nil
}

// versionString is the name, version and commit the binary was built from, as the version command prints it and serve starts by saying.
// A build past a tag already names its commit in the version, so the commit is only added when it is not there.
func versionString() string {
	s := "sas-exporter " + version.Version
	if version.GitCommit != "" && !strings.Contains(version.Version, version.GitCommit) {
		s += " (" + version.GitCommit + ")"
	}

	return s
}
