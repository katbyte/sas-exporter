package cli

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/katbyte/go-kt/prometheus/exporter"
	"github.com/spf13/cobra"
)

func serveCmd(connect connector) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the metrics page",
		Long: `Asks the kernel about the machine's SAS cards on a schedule and serves the last answers at
/metrics in the Prometheus text format, with a health probe at /healthz.

A scrape is answered from memory and never waits on a card. The cards, their expanders, every
phy with its link and error counts, and the disks are read every minute; each enclosure is
asked about its slots every five minutes. A card that stops answering is reported as
sas_card_up 0, and does not stop another being reported. --intervals changes how often each is
asked: links=2m,enclosures=10m,sensors=2m.

All of that is read from the kernel's /sys, which needs no root and opens no device. --sysfs
says where that is, for a container given the host's somewhere else.

What the kernel does not keep is asked of the hardware itself every minute: each
enclosure for its fans, power supplies, temperatures and voltages, and each card for its own
temperature, its connectors and how often each link has changed. That means opening device
files under --dev, which takes root. Without leave to, the rest is reported without them.

Everything is reported unless --skip leaves it out: a list of poll, cards, expanders, phys,
phy-errors, link-changes, devices, enclosures, slots, card-sensors and enclosure-sensors. A
part left out is not asked for either: --skip phy-errors,slots spares the expanders and the
enclosures those questions. --metrics names the only parts to ask about instead, and keeps
what each is within: --metrics slots asks about the enclosures' slots and nothing else. Whether
each card is up is always reported.

Every reading says which card it hangs off in its card label, by the card's model: 9500-16i.
--card-names gives a card a name of your choosing in place of that, as 9500-16e=jbod, where a
card is named by what it goes by without one or by its SAS address.

--labels puts labels of your choosing on every reading, hostname=nas say, for when whatever
scrapes the page does not say whose it is.

Every setting is also an environment variable: SAS_EXPORTER_SYSFS, SAS_EXPORTER_DEV,
SAS_EXPORTER_LISTEN, SAS_EXPORTER_LABELS, SAS_EXPORTER_METRICS, SAS_EXPORTER_SKIP,
SAS_EXPORTER_CARD_NAMES and SAS_EXPORTER_INTERVALS.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			// said before anything else, so a log shows which build it came from
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), versionString())

			f := GetFlags()
			poller, err := f.poller(connect)
			if err != nil {
				return err
			}

			p, err := f.page(poller)
			if err != nil {
				return err
			}

			// one signal stops both the polling and the serving
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return exporter.Serve(ctx, f.Listen, poller.Inner(), p)
		},
	}
}
