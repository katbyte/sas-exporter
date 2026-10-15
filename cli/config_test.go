package cli

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/poll/polltest"
	"github.com/katbyte/sas-exporter/lib/sysfs/sysfstest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// viper is global, so none of these can run in parallel and each resets it.
const testUse = "sas-exporter"

// alone cuts a test off from the settings of whoever runs it: viper as it starts, an empty home and working directory, and none of the program's
// variables set.
func alone(t *testing.T) (home, wd string) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)

	home, wd = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(wd)
	t.Setenv(configEnv, "")
	for _, env := range envNames {
		t.Setenv(env, "")
	}

	return home, wd
}

// load drives the real flag wiring with these command-line arguments, after whatever the test put in the environment and the settings files.
func load(t *testing.T, args ...string) (*FlagData, error) {
	t.Helper()

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatalf("configureFlags: %v", err)
	}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := loadConfig(); err != nil {
		return nil, err
	}

	return GetFlags(), nil
}

// must is load for a test that expects the settings to be read.
func must(t *testing.T, args ...string) *FlagData {
	t.Helper()

	f, err := load(t, args...)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	return f
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// With nothing set the page is served where the README says, the kernel is read where it always is, and as often as the README says: these are
// what a dashboard's scrape interval and an example compose file are written against.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestDefaults(t *testing.T) {
	alone(t)

	want := FlagData{Sysfs: "/sys", Dev: "/dev", Listen: ":9088", Sensors: "all"}
	if got := *must(t); got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
}

// Every setting has an environment variable, which is how a container is configured.
func TestEveryFlagHasAnEnvironmentVariable(t *testing.T) {
	alone(t)

	t.Setenv("SAS_EXPORTER_SYSFS", "/host/sys")
	t.Setenv("SAS_EXPORTER_DEV", "/host/dev")
	t.Setenv("SAS_EXPORTER_LISTEN", "127.0.0.1:9000")
	t.Setenv("SAS_EXPORTER_LABELS", "hostname=nas")
	t.Setenv("SAS_EXPORTER_SENSORS", "enclosures")
	t.Setenv("SAS_EXPORTER_INTERVALS", "links=45s,enclosures=10m,sensors=2m")
	t.Setenv("SAS_EXPORTER_CARD_NAMES", "9500-16e=jbod")

	want := FlagData{Sysfs: "/host/sys", Dev: "/host/dev", Listen: "127.0.0.1:9000", Labels: "hostname=nas", Sensors: "enclosures", CardNames: "9500-16e=jbod", Intervals: "links=45s,enclosures=10m,sensors=2m"}
	if got := *must(t); got != want {
		t.Errorf("from the environment = %+v, want %+v", got, want)
	}

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatal(err)
	}
	root.PersistentFlags().VisitAll(func(fl *pflag.Flag) {
		if _, ok := envNames[fl.Name]; !ok && fl.Name != "config" {
			t.Errorf("--%s has no environment variable", fl.Name)
		}
	})
}

// A flag beats the environment, which beats a settings file: a container overrides what an image holds, and a command line overrides both.
func TestFlagThenEnvironmentThenFile(t *testing.T) {
	home, _ := alone(t)
	write(t, home, ".sas-exporter", "SYSFS=/from-file\nLABELS=from=file\nCARD_NAMES=9500-16e=file\nLISTEN=:9000\n")

	f := must(t)
	if f.Sysfs != "/from-file" || f.Labels != "from=file" || f.CardNames != "9500-16e=file" || f.Listen != ":9000" {
		t.Errorf("from the file = %+v, want every setting it holds, the one of two words included", f)
	}

	viper.Reset()
	t.Setenv("SAS_EXPORTER_SYSFS", "/from-env")
	t.Setenv("SAS_EXPORTER_CARD_NAMES", "9500-16e=env")
	f = must(t)
	if f.Sysfs != "/from-env" || f.CardNames != "9500-16e=env" || f.Labels != "from=file" {
		t.Errorf("with the environment = %+v, want it over the file, and the file for the rest", f)
	}

	viper.Reset()
	f = must(t, "--sysfs", "/from-flag", "--card-names", "9500-16e=flag")
	if f.Sysfs != "/from-flag" || f.CardNames != "9500-16e=flag" {
		t.Errorf("with flags = %+v, want them over the environment", f)
	}
}

// A .sas-exporter in the working directory is found before the one in $HOME, and neither being there is not an error.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestSettingsFilePrecedence(t *testing.T) {
	home, wd := alone(t)

	if f := must(t); f.Labels != "" {
		t.Errorf("labels = %q with no settings file, want none", f.Labels)
	}

	viper.Reset()
	write(t, home, ".sas-exporter", "LABELS=from=home\n")
	if f := must(t); f.Labels != "from=home" {
		t.Errorf("labels = %q, want the ones in $HOME", f.Labels)
	}

	viper.Reset()
	write(t, wd, ".sas-exporter", "LABELS=from=here\n")
	if f := must(t); f.Labels != "from=here" {
		t.Errorf("labels = %q, want the working directory's over $HOME's", f.Labels)
	}
}

// A settings file that is named is the only one read, and one that cannot be read is an error: a run told where its settings are must not come
// up with someone else's, or with none.
func TestANamedSettingsFile(t *testing.T) {
	home, _ := alone(t)
	write(t, home, ".sas-exporter", "LABELS=from=home\n")
	named := write(t, home, "other.env", "LISTEN=:9001\n")

	f := must(t, "--config", named)
	if f.Listen != ":9001" || f.Labels != "" {
		t.Errorf("with --config = %+v, want the named file and nothing of $HOME's", f)
	}

	viper.Reset()
	t.Setenv(configEnv, "~/other.env")
	if f := must(t); f.Listen != ":9001" {
		t.Errorf("with %s=~/other.env the page is served on %q, want the named file's", configEnv, f.Listen)
	}

	viper.Reset()
	if _, err := load(t, "--config", filepath.Join(home, "missing.env")); err == nil || !strings.Contains(err.Error(), "missing.env") {
		t.Errorf("a named file that is not there = %v, want an error naming it", err)
	}
}

// The poller is made with what asks the kernel where the settings say its /sys is, and a place that cannot be read is said with the setting that
// names it. An interval of nothing is refused.
func TestPoller(t *testing.T) {
	t.Parallel()

	f := FlagData{Sysfs: "/host/sys", Dev: "/host/dev", Sensors: "all", Intervals: "links=1s"}

	var asked []source
	p, err := f.poller(func(from source) (poll.Kernel, error) {
		asked = append(asked, from)
		return &polltest.Kernel{}, nil
	})
	if err != nil || p == nil {
		t.Fatalf("poller = %v (%v), want one", p, err)
	}
	if want := (source{sysfs: "/host/sys", dev: "/host/dev", enclosures: true, card: true}); len(asked) != 1 || asked[0] != want {
		t.Errorf("the kernel was looked for with %+v, want %+v", asked, want)
	}

	_, err = f.poller(func(source) (poll.Kernel, error) { return nil, errors.New("no such directory") })
	if err == nil || !strings.Contains(err.Error(), "no such directory") || !strings.Contains(err.Error(), "SAS_EXPORTER_SYSFS") {
		t.Errorf("a /sys that cannot be read = %v, want the error with the setting that names it", err)
	}

	f.Intervals = "sensors=0s"
	if _, err := f.poller(func(source) (poll.Kernel, error) { return &polltest.Kernel{}, nil }); err == nil || !strings.Contains(err.Error(), "SAS_EXPORTER_INTERVALS") {
		t.Errorf("an interval of nothing = %v, want it refused, naming the setting", err)
	}
}

// Names chosen for cards are a list of a card and its name. One that is not written so, a card named twice, or one name for two cards stops the
// program, naming the setting: two cards under one name would be one card on the page.
func TestTheNamesChosenForCards(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]poll.Names{
		"":              {},
		"9500-16e=jbod": {"9500-16e": "jbod"},
		"9500-16i=inside, 0x500062B000000002 = jbod ,": {"9500-16i": "inside", "0x500062B000000002": "jbod"},
	} {
		f := FlagData{CardNames: said}
		if got, err := f.names(); err != nil || !maps.Equal(got, want) {
			t.Errorf("--card-names %q = %v (%v), want %v", said, got, err, want)
		}
	}

	for _, said := range []string{"jbod", "9500-16e=", "=jbod", "9500-16e=jbod,9500-16e=shelf", "9500-16e=jbod,9500-16i=jbod"} {
		f := FlagData{CardNames: said}
		if _, err := f.names(); err == nil || !strings.Contains(err.Error(), "SAS_EXPORTER_CARD_NAMES") {
			t.Errorf("--card-names %q = %v, want it refused, naming the setting", said, err)
		}
	}
}

// How often each part is asked about is one list, --intervals, as in every exporter of this kind, over the defaults. A part nobody has heard of,
// one named twice, or an interval of nothing stops the program, naming the setting.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestHowOftenEachPartIsAsked(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		env  map[string]string
		file string
		want poll.Intervals
	}{
		{name: "nothing set", want: poll.Intervals{Links: time.Minute, Enclosures: 5 * time.Minute, Sensors: time.Minute}},
		{name: "the list", args: []string{"--intervals", "links=2m, sensors=30s"}, want: poll.Intervals{Links: 2 * time.Minute, Enclosures: 5 * time.Minute, Sensors: 30 * time.Second}},
		{name: "the list in the environment", env: map[string]string{"SAS_EXPORTER_INTERVALS": "enclosures=10m"}, want: poll.Intervals{Links: time.Minute, Enclosures: 10 * time.Minute, Sensors: time.Minute}},
		{name: "the list in the settings file", file: "INTERVALS=links=45s\n", want: poll.Intervals{Links: 45 * time.Second, Enclosures: 5 * time.Minute, Sensors: time.Minute}},
	} {
		home, _ := alone(t)
		for env, value := range c.env {
			t.Setenv(env, value)
		}
		if c.file != "" {
			write(t, home, ".sas-exporter", c.file)
		}

		got, err := must(t, c.args...).every()
		if err != nil || got != c.want {
			t.Errorf("%s: %+v (%v), want %+v", c.name, got, err, c.want)
		}
	}

	for _, c := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{args: []string{"--intervals", "links"}, want: "SAS_EXPORTER_INTERVALS"},
		{args: []string{"--intervals", "disks=1m"}, want: "SAS_EXPORTER_INTERVALS"},
		{args: []string{"--intervals", "links=0s"}, want: "SAS_EXPORTER_INTERVALS"},
		{args: []string{"--intervals", "links=fast"}, want: "SAS_EXPORTER_INTERVALS"},
		{args: []string{"--intervals", "links=1m,links=2m"}, want: "SAS_EXPORTER_INTERVALS"},
	} {
		alone(t)
		for env, value := range c.env {
			t.Setenv(env, value)
		}

		if got, err := must(t, c.args...).every(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v with %v = %+v (%v), want it refused, naming %s", c.args, c.env, got, err, c.want)
		}
	}
}

// Which of the hardware is asked itself is a list, so one kind can be asked and the other left alone: a card whose temperature is not wanted
// need never be asked through its driver. A word that is none of them stops the program, naming the setting.
func TestWhichHardwareIsAsked(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		said string
		want source
	}{
		{"all", source{enclosures: true, card: true}},
		{"enclosures", source{enclosures: true}},
		{"cards", source{card: true}},
		{"enclosures,cards", source{enclosures: true, card: true}},
		{" cards , enclosures", source{enclosures: true, card: true}},
		{"none", source{}},
		{"", source{}},
	} {
		f := FlagData{Sensors: c.said}
		if got, err := f.source(); err != nil || got != c.want {
			t.Errorf("--sensors %q = %+v (%v), want %+v", c.said, got, err, c.want)
		}
	}

	for _, said := range []string{"fans", "all,fans", "enclosures;cards", "hba"} {
		f := FlagData{Sensors: said}
		if _, err := f.source(); err == nil || !strings.Contains(err.Error(), "SAS_EXPORTER_SENSORS") {
			t.Errorf("--sensors %q = %v, want it refused, naming the setting", said, err)
		}
	}
}

// What the program runs with reads a directory, and says at once when what it was given is not one: a wrong path would otherwise look the same as
// a machine with no card.
func TestConnect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if k, err := connect(source{sysfs: dir}); err != nil || k == nil {
		t.Errorf("connect to a directory = %v (%v), want a kernel", k, err)
	}

	if k, err := connect(source{sysfs: filepath.Join(dir, "missing")}); err == nil || k != nil {
		t.Errorf("connect to nothing = %v (%v), want an error", k, err)
	}

	if k, err := connect(source{sysfs: write(t, dir, "file", "")}); err == nil || k != nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("connect to a file = %v (%v), want it refused as not a directory", k, err)
	}
}

// The hardware is only ever asked for what the settings say to ask it. Told to ask for nothing, the reader opens no device and has nothing to
// say went unread; told to ask, it looks for the devices where it was told they are, here a directory with none in it.
func TestConnectAsksOnlyWhatItIsToldTo(t *testing.T) {
	t.Parallel()

	root, _ := sysfstest.NAS(t)
	for _, from := range []source{{enclosures: true}, {card: true}, {enclosures: true, card: true}, {}} {
		from.sysfs, from.dev = root, t.TempDir()

		k, err := connect(from)
		if err != nil {
			t.Fatalf("connect %+v: %v", from, err)
		}
		got, err := k.Sensors(t.Context(), "host1")
		if err != nil || !got.Nothing() || got.Unread != nil {
			t.Fatalf("with %+v the sensors = %+v (%v), want none, and nothing said to have failed", from, got, err)
		}

		if said := strings.Contains(got.Unavailable, "enclosures"); said != from.enclosures {
			t.Errorf("with %+v what could not be asked = %q, which names the enclosures: %t", from, got.Unavailable, said)
		}
		if said := strings.Contains(got.Unavailable, "card"); said != from.card {
			t.Errorf("with %+v what could not be asked = %q, which names the card: %t", from, got.Unavailable, said)
		}
	}
}
