package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/poller"
	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/readings"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The defaults. Links are read every minute: each phy's error counts are fetched from the card or the expander as they are read, which is too
// much to do for every scrape and often enough to see a count grow. Slots are read every few minutes, since each reading of one has the kernel
// fetch its enclosure's whole status and a slot seldom changes. Sensors are read every minute, which is four questions of each enclosure and two
// of each card.
const (
	defaultSysfs              = "/sys"
	defaultDev                = "/dev"
	defaultListen             = ":9088"
	defaultSensors            = sensorsAll
	defaultLinksInterval      = time.Minute
	defaultEnclosuresInterval = 5 * time.Minute
	defaultSensorsInterval    = time.Minute
)

// What --sensors can name: which of the hardware is asked itself for what the kernel does not keep.
const (
	sensorsAll        = "all"
	sensorsEnclosures = "enclosures"
	sensorsCards      = "cards"
	sensorsNone       = "none"
)

type FlagData struct {
	Sysfs     string `mapstructure:"sysfs"`
	Dev       string `mapstructure:"dev"`
	Listen    string `mapstructure:"listen"`
	Labels    string `mapstructure:"labels"`
	Sensors   string `mapstructure:"sensors"`
	CardNames string `mapstructure:"card-names"`
	Intervals string `mapstructure:"intervals"`
}

// envNames binds each flag to its environment variable.
var envNames = map[string]string{
	"sysfs":      "SAS_EXPORTER_SYSFS",
	"dev":        "SAS_EXPORTER_DEV",
	"listen":     "SAS_EXPORTER_LISTEN",
	"labels":     "SAS_EXPORTER_LABELS",
	"sensors":    "SAS_EXPORTER_SENSORS",
	"card-names": "SAS_EXPORTER_CARD_NAMES",
	"intervals":  "SAS_EXPORTER_INTERVALS",
}

// persistent is the flag set configureFlags bound, for the one setting that has to be read before viper is: where the settings file is.
var persistent *pflag.FlagSet

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.String("sysfs", defaultSysfs, "where the kernel's /sys is: /host/sys say, for a container given the host's there")
	pflags.String("dev", defaultDev, "where the machine's device files are, for what is asked of the hardware itself")
	pflags.String("listen", defaultListen, "serve the metrics page on this address")
	pflags.String("labels", "", "labels to put on every reading, as name=value,name=value: hostname=nas say, when whatever scrapes the page does not add its own")
	pflags.String("sensors", defaultSensors, "what is asked of the hardware itself, which takes leave to open its devices: all, none, or any of enclosures (fans, power supplies, temperatures, voltages) and cards (each card's own temperature) with a comma between")
	pflags.String("card-names", "", "names for the cards in place of the ones they go by, as card=name,card=name: 9500-16e=jbod say, where a card is named by what it goes by without one or by its SAS address")
	pflags.String("intervals", "", "how often each part is asked about, as part=interval,part=interval: links (the cards, phys, error counts and disks, every 1m), enclosures (the slots, every 5m) and sensors (fans and temperatures, every 1m)")
	pflags.String("config", "", "read settings from this file and no other, in place of ./.sas-exporter or ~/.sas-exporter (or export SAS_EXPORTER_CONFIG)")

	persistent = pflags

	for name, env := range envNames {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}

		if err := viper.BindEnv(name, env); err != nil {
			return fmt.Errorf("error binding '%s' to env '%s' : %w", name, env, err)
		}
	}

	return nil
}

// configEnv names the settings file from the environment, as --config does on the command line.
const configEnv = "SAS_EXPORTER_CONFIG"

// configPath is the settings file a run was told to read, "" for none: the flag's, then the environment's. A leading ~/ is the home directory, for
// whatever starts the program without a shell to expand it.
func configPath() string {
	path := os.Getenv(configEnv)
	if persistent != nil {
		if fl := persistent.Lookup("config"); fl != nil && fl.Changed {
			path = fl.Value.String()
		}
	}

	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}

	return path
}

// loadConfig reads the settings file, once the flags are parsed: the one --config or SAS_EXPORTER_CONFIG names and no other, or else the first
// .sas-exporter found in the working directory and then in $HOME. A file that was named and cannot be read is an error; one merely absent from the
// usual places is not.
func loadConfig() error {
	viper.SetConfigType("env")
	if path := configPath(); path != "" {
		viper.SetConfigFile(path)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("reading the settings file %s (--config / %s): %w", path, configEnv, err)
		}
	} else {
		viper.SetConfigName(".sas-exporter")
		// viper reads the first file it finds, so the working directory comes first
		viper.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(home)
		}

		if err := viper.ReadInConfig(); err != nil {
			if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
				clog.Log.Errorf("Error reading config file: %v", err)
			}
		}
	}

	// a settings file spells a setting the way the environment does, less the prefix (CARD_NAMES for --card-names), and viper only matches a
	// key spelled as the flag is. Carried across as defaults, they still lose to a flag or the environment.
	for name := range envNames {
		if alt := strings.ReplaceAll(name, "-", "_"); alt != name && viper.InConfig(alt) {
			viper.SetDefault(name, viper.Get(alt))
		}
	}

	return nil
}

// GetFlags returns the fully populated FlagData. It is read from viper rather than from the flags themselves, since viper is what lays the
// environment and the settings file under the command line.
func GetFlags() *FlagData {
	var f FlagData
	if err := viper.Unmarshal(&f); err != nil {
		clog.Log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	return &f
}

// source is where the cards are read from, and which of the hardware is asked itself.
type source struct {
	// sysfs is where the kernel's /sys is, and dev where the machine's device files are.
	sysfs string
	dev   string
	// enclosures and card say whether each is asked for what the kernel does not keep.
	enclosures bool
	card       bool
}

// source is what the settings say to read. --sensors is a list, so that one kind of hardware can be asked and the other left alone.
func (f *FlagData) source() (source, error) {
	s := source{sysfs: f.Sysfs, dev: f.Dev}
	for named := range strings.SplitSeq(f.Sensors, ",") {
		switch named = strings.TrimSpace(named); named {
		case sensorsAll:
			s.enclosures, s.card = true, true
		case sensorsEnclosures:
			s.enclosures = true
		case sensorsCards:
			s.card = true
		case sensorsNone, "":
		default:
			return source{}, fmt.Errorf("--sensors (%s): %q is none of %s, %s, %s and %s", envNames["sensors"], named, sensorsAll, sensorsEnclosures, sensorsCards, sensorsNone)
		}
	}

	return s, nil
}

// names is the names the settings choose for cards. Two cards under one name would be one card on the page, so a name can be given once.
func (f *FlagData) names() (poll.Names, error) {
	names, taken := poll.Names{}, map[string]string{}
	for pair := range strings.SplitSeq(f.CardNames, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}

		card, name, found := strings.Cut(pair, "=")
		card, name = strings.TrimSpace(card), strings.TrimSpace(name)
		if !found || card == "" || name == "" {
			return nil, fmt.Errorf("--card-names (%s): %q is not a card and a name for it, as card=name", envNames["card-names"], pair)
		}
		if _, twice := names[card]; twice {
			return nil, fmt.Errorf("--card-names (%s): %s is given a name twice", envNames["card-names"], card)
		}
		if other, twice := taken[name]; twice {
			return nil, fmt.Errorf("--card-names (%s): %s and %s cannot both be called %s", envNames["card-names"], other, card, name)
		}

		names[card], taken[name] = name, card
	}

	return names, nil
}

// every is how often each part is asked about: the defaults, with what --intervals says in place of any of them.
func (f *FlagData) every() (poll.Intervals, error) {
	every, err := poller.ParseIntervals(f.Intervals, map[string]time.Duration{string(poll.RoundLinks): defaultLinksInterval, string(poll.RoundEnclosures): defaultEnclosuresInterval, string(poll.RoundSensors): defaultSensorsInterval})
	if err != nil {
		return poll.Intervals{}, fmt.Errorf("--intervals (%s): %w", envNames["intervals"], err)
	}

	return poll.Intervals{Links: every[string(poll.RoundLinks)], Enclosures: every[string(poll.RoundEnclosures)], Sensors: every[string(poll.RoundSensors)]}, nil
}

// connector makes what asks the kernel about its SAS cards.
type connector func(source) (poll.Kernel, error)

// poller is the poller of this machine's cards, not yet started.
func (f *FlagData) poller(connect connector) (*poll.Poller, error) {
	from, err := f.source()
	if err != nil {
		return nil, err
	}

	names, err := f.names()
	if err != nil {
		return nil, err
	}

	every, err := f.every()
	if err != nil {
		return nil, err
	}

	kernel, err := connect(from)
	if err != nil {
		return nil, fmt.Errorf("--sysfs (%s): %w", envNames["sysfs"], err)
	}

	p, err := poll.New(kernel, every, names)
	if err != nil {
		return nil, err
	}

	clog.Log.Infof("reading the kernel's SAS cards under %s, links every %s, enclosures every %s and sensors every %s", f.Sysfs, every.Links, every.Enclosures, every.Sensors)

	return p, nil
}

// name is what the program is called, on the page's front and in its help.
const name = "sas-exporter"

// page is the metrics page of the poller's readings, with the labels the settings ask for on every one. A label that cannot be used is said here,
// before the kernel is asked anything: one written wrongly, or one named as a reading's own label is, card say.
func (f *FlagData) page(asking *poll.Poller) (*metrics.Page, error) {
	labels, err := metrics.ParseLabels(f.Labels)
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	p, err := metrics.New(metrics.Options{Name: name, Labels: labels}, readings.New(asking), asking.Inner())
	if err != nil {
		return nil, fmt.Errorf("--labels (%s): %w", envNames["labels"], err)
	}

	return p, nil
}
