package poll

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/prometheus/poller"
)

// Round is one of the three schedules the kernel is asked on.
type Round string

const (
	// RoundLinks is the cards, their expanders and phys, and the devices at the ends: asked often enough to watch an error count grow.
	RoundLinks Round = "links"
	// RoundEnclosures is each enclosure's slots, which seldom change and which the enclosure itself has to be asked about.
	RoundEnclosures Round = "enclosures"
	// RoundSensors is what the cards and the enclosures measure of themselves: temperatures, fans, power supplies. It is asked of the hardware
	// itself, and only where there is leave to.
	RoundSensors Round = "sensors"
)

// staleAfter is how many intervals an answer is served for once its round stops succeeding: the same as go-kt's poller holds every other answer to,
// so a card that has gone loses what it said of itself along with the rest.
const staleAfter = 3

// Intervals is how often each round runs. A round is also given that long to finish, so one never overlaps the next.
type Intervals struct {
	Links      time.Duration
	Enclosures time.Duration
	Sensors    time.Duration
}

// Answer is the last good answer to one question.
type Answer[T any] struct {
	Value T
	// At is when it was given, and the zero time for a question never answered.
	At time.Time
}

// Known says whether there is an answer to serve.
func (a Answer[T]) Known() bool { return !a.At.IsZero() }

// RoundState is how one round last went for one card.
type RoundState struct {
	// Ran says a round has finished since the start; until one has, the rest says nothing.
	Ran bool
	// OK says every question of the last round was answered in full.
	OK   bool
	Took time.Duration
	// LastSuccess is the zero time until a round succeeds.
	LastSuccess time.Time
	// Failures counts the rounds that failed since the start.
	Failures int
}

// Card is what is known of one card: the last good answer to each question, and how the rounds that asked went.
type Card struct {
	// Name is the kernel's name for the card, host1, which is what it is asked by. Called is what it goes by on the page: see Names.
	Name   string
	Called string
	// Present says the kernel listed the card the last time it was asked which cards it has. One that has gone stays known, so it can be reported
	// as down rather than vanish.
	Present    bool
	HBA        Answer[HBA]
	Links      Answer[Links]
	Enclosures Answer[[]Enclosure]
	Sensors    Answer[Sensors]

	LinksRound      RoundState
	EnclosuresRound RoundState
	SensorsRound    RoundState
}

// Up says whether the kernel has the card and has it in service.
func (c *Card) Up() bool { return c.Present && c.HBA.Known() && c.HBA.Value.Running }

// State is what is known of every card now.
type State struct {
	Cards []Card
}

// Names is the names chosen for cards in place of the ones they would go by: each by the name the card would have without it, or by its SAS
// address.
//
// Left to itself a card goes by its model, less the word that says only that it is a card: 9500-16i. One that does not say what it is goes by the
// kernel's name for it. Cards that would share a name each have the kernel's number for them added, since two under one name would be one on the
// page.
type Names map[string]string

// modelWord is the word some cards begin their model with that says only that they are cards.
const modelWord = "HBA "

// plain is the name a card goes by when none is chosen for it.
func plain(card poller.Target[HBA]) string {
	if model := strings.TrimSpace(strings.TrimPrefix(card.Value.Model, modelWord)); model != "" {
		return strings.ReplaceAll(model, " ", "-")
	}

	return card.Key
}

// addressed says a key of Names is this card's SAS address, written as the kernel writes it or without the 0x.
func addressed(key string, card poller.Target[HBA]) bool {
	return card.Value.SASAddress != "" && strings.EqualFold(strings.TrimPrefix(strings.ToLower(key), "0x"), card.Value.SASAddress)
}

// chosen is the name chosen for a card, if one was: by its SAS address, or else by the name it would have.
func (n Names) chosen(card poller.Target[HBA]) (string, bool) {
	for key, name := range n {
		if addressed(key, card) {
			return name, true
		}
	}

	name, found := n[plain(card)]

	return name, found
}

// idle is the cards names were chosen for that are none of these, in order.
func (n Names) idle(cards map[string]poller.Target[HBA]) []string {
	var out []string
	for key := range n {
		named := false
		for _, card := range cards {
			named = named || key == plain(card) || addressed(key, card)
		}
		if !named {
			out = append(out, key)
		}
	}
	slices.Sort(out)

	return out
}

// Poller asks the kernel about each card on three schedules and keeps the last good answers. go-kt's poller does the asking, the keeping and the
// readings of how it is going; this says what is asked, what a card is called, and gives what was heard as a State for the readings.
type Poller struct {
	inner      *poller.Poller[HBA]
	links      *poller.Kept[Links]
	enclosures *poller.Kept[[]Enclosure]
	sensors    *poller.Kept[Sensors]
	every      Intervals
	given      Names

	mu sync.Mutex
	// unavailable is what each card last said could not be asked here, so that it is logged when it changes and not every round
	unavailable map[string]string
	// unmatched says the names chosen for no card have been logged, which is done once
	unmatched bool
}

// New is a poller of one machine's cards, each going by the name chosen for it when one is. It asks nothing until Run or Poll.
func New(kernel Kernel, every Intervals, names Names) (*Poller, error) {
	if every.Links <= 0 || every.Enclosures <= 0 || every.Sensors <= 0 {
		return nil, fmt.Errorf("the intervals must be longer than nothing: links %s, enclosures %s, sensors %s", every.Links, every.Enclosures, every.Sensors)
	}

	p := &Poller{every: every, given: names, unavailable: map[string]string{}}
	inner, err := poller.New(poller.Options[HBA]{Namespace: "sas", Label: "card", Kind: "SAS card", Targets: kernel.HBAs, Key: func(h HBA) string { return h.Name }, Names: p.called})
	if err != nil {
		return nil, err
	}
	p.inner = inner

	// links read part way are kept, with the round failed: the phys whose counts could not be read are on the page without them
	p.links = poller.Ask(inner.Schedule(string(RoundLinks), every.Links), "links", func(ctx context.Context, card HBA) (Links, error) {
		links, err := kernel.Links(ctx, card.Name)
		if err != nil {
			return links, err
		}

		return links, poller.Partial(links.Unread)
	})

	p.enclosures = poller.Ask(inner.Schedule(string(RoundEnclosures), every.Enclosures), "enclosures", func(ctx context.Context, card HBA) ([]Enclosure, error) {
		return kernel.Enclosures(ctx, card.Name)
	})

	p.sensors = poller.Ask(inner.Schedule(string(RoundSensors), every.Sensors), "sensors", p.sensorsOf(kernel))

	return p, nil
}

// sensorsOf asks a card for what it and its enclosures measure. What there was no leave to ask is said in the log when it changes, and a card with
// nothing that could be asked has a round that is counted neither way.
func (p *Poller) sensorsOf(kernel Kernel) func(ctx context.Context, card HBA) (Sensors, error) {
	return func(ctx context.Context, card HBA) (Sensors, error) {
		sensors, err := kernel.Sensors(ctx, card.Name)
		if err != nil {
			return sensors, err
		}

		p.mu.Lock()
		before := p.unavailable[card.Name]
		p.unavailable[card.Name] = sensors.Unavailable
		p.mu.Unlock()
		if sensors.Unavailable != "" && sensors.Unavailable != before {
			clog.Log.Warnf("card %s (%s): not everything it measures can be read from here, and the rest is reported without it: %s", p.inner.Names()[card.Name], card.Name, sensors.Unavailable)
		}

		if sensors.Nothing() {
			return sensors, poller.ErrNothing
		}

		return sensors, poller.Partial(sensors.Unread)
	}
}

// Inner is go-kt's poller underneath: a collector of the readings of how the asking is going, and what exporter.Print and Serve run.
func (p *Poller) Inner() *poller.Poller[HBA] { return p.inner }

// Run polls until the context ends: every round at once on start, then each on its own interval.
func (p *Poller) Run(ctx context.Context) { p.inner.Run(ctx) }

// Poll runs one round now, for every card at once.
func (p *Poller) Poll(ctx context.Context, round Round) { _ = p.inner.Poll(ctx, string(round)) }

// called is what each card goes by on the page, given every card ever seen, gone ones too: numbering two of a model needs them all, or the one
// still there would change its name as the other went.
func (p *Poller) called(cards map[string]poller.Target[HBA]) map[string]string {
	names, shared := make(map[string]string, len(cards)), map[string]int{}
	for kernel, card := range cards {
		name, found := p.given.chosen(card)
		if !found {
			name = plain(card)
		}

		names[kernel] = name
		shared[name]++
	}

	for kernel, name := range names {
		if shared[name] < 2 {
			continue
		}

		// host8 is 8, and a name with no number in it is used whole
		number := strings.TrimLeft(kernel, "abcdefghijklmnopqrstuvwxyz")
		if number == "" {
			number = kernel
		}
		names[kernel] = name + "-" + number
	}

	// names chosen for cards that are not here are said the once, the first time there are cards to hold them against
	p.mu.Lock()
	var idle []string
	if !p.unmatched && len(cards) > 0 {
		p.unmatched, idle = true, p.given.idle(cards)
	}
	p.mu.Unlock()
	if len(idle) > 0 {
		clog.Log.Warnf("a name was chosen for %s, which is no card this machine has: a card is named by what it goes by without one, or by its SAS address", strings.Join(idle, ", "))
	}

	return names
}

// State is what is known of every card now, without asking anything. What a card said of itself is left off once three links intervals old, as
// every other answer is.
func (p *Poller) State() State {
	names := p.inner.Names()
	keys := p.inner.Targets()

	s := State{Cards: make([]Card, 0, len(keys))}
	for _, key := range keys {
		target := p.inner.Target(key)
		card := Card{Name: key, Called: names[key], Present: target.Listed}
		if !target.At.IsZero() && time.Since(target.At) <= staleAfter*p.every.Links {
			card.HBA = Answer[HBA]{Value: target.Value, At: target.At}
		}

		card.Links = Answer[Links](p.links.For(key))
		card.Enclosures = Answer[[]Enclosure](p.enclosures.For(key))
		card.Sensors = Answer[Sensors](p.sensors.For(key))
		card.LinksRound = RoundState(p.inner.Round(key, string(RoundLinks)))
		card.EnclosuresRound = RoundState(p.inner.Round(key, string(RoundEnclosures)))
		card.SensorsRound = RoundState(p.inner.Round(key, string(RoundSensors)))

		s.Cards = append(s.Cards, card)
	}

	return s
}
