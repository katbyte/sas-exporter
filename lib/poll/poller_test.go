package poll_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/sas-exporter/lib/poll"
	"github.com/katbyte/sas-exporter/lib/poll/polltest"
)

// The two cards the tests poll, by the kernel's names for them.
const (
	inside  = "host1"
	outside = "host8"
)

// every is how often the tests poll: the program's own defaults.
var every = poll.Intervals{Links: time.Minute, Enclosures: 5 * time.Minute, Sensors: time.Minute}

// errReset is a card that will not answer because it is being reset.
var errReset = errors.New("host reset in progress")

func newPoller(t *testing.T, kernel poll.Kernel) *poll.Poller {
	t.Helper()

	p, err := poll.New(kernel, every, nil)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// card is what the poller knows of one card, and fails the test when it knows of none by that name.
func card(t *testing.T, p *poll.Poller, name string) *poll.Card {
	t.Helper()

	cards := p.State().Cards
	for i := range cards {
		if cards[i].Name == name {
			return &cards[i]
		}
	}
	t.Fatalf("the poller knows of no card called %s", name)

	return nil
}

// twoCards is a kernel with both cards in service, a phy and a disk on the first and a shelf of two slots on the second.
func twoCards() *polltest.Kernel {
	return &polltest.Kernel{
		Cards:            []poll.HBA{{Name: inside, Model: "HBA 9500-16i", Running: true}, {Name: outside, Model: "HBA 9500-16e", Running: true}},
		LinksAnswer:      map[string]poll.Links{inside: {Phys: []poll.Phy{{Number: 0, Link: poll.LinkUp}}, Devices: []poll.Device{{Serial: "Z0000001"}}}},
		EnclosuresAnswer: map[string][]poll.Enclosure{outside: {{Name: "8:0:24:0", Slots: make([]poll.Slot, 2)}}},
	}
}

// A round keeps what the kernel said of each card, and how long the asking took, and leaves the other round's question unasked.
func TestARoundKeepsItsAnswers(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.Delay(polltest.Links, 2*time.Second)
		p := newPoller(t, kernel)

		start := time.Now()
		p.Poll(t.Context(), poll.RoundLinks)

		if cards := p.State().Cards; len(cards) != 2 || cards[0].Name != inside || cards[1].Name != outside {
			t.Fatalf("cards = %+v, want the two the kernel has, in the order of their names", cards)
		}

		first := card(t, p, inside)
		if r := first.LinksRound; !r.Ran || !r.OK || r.Took != 2*time.Second || !r.LastSuccess.Equal(start.Add(2*time.Second)) || r.Failures != 0 {
			t.Errorf("links round = %+v, want one that ran, answered and took 2s", r)
		}
		if !first.Up() || !first.HBA.Known() || first.HBA.Value.Model != "HBA 9500-16i" {
			t.Errorf("the card = %+v, want it up with what the kernel said of it", first.HBA)
		}
		if !first.Links.Known() || len(first.Links.Value.Phys) != 1 || len(first.Links.Value.Devices) != 1 {
			t.Errorf("links = %+v, want the phy and the disk", first.Links)
		}

		if first.EnclosuresRound.Ran || first.Enclosures.Known() || kernel.Asked(polltest.Enclosures) != 0 {
			t.Errorf("the enclosures round ran with the links one: %+v", first.EnclosuresRound)
		}

		p.Poll(t.Context(), poll.RoundEnclosures)
		if second := card(t, p, outside); !second.EnclosuresRound.OK || len(second.Enclosures.Value) != 1 || len(second.Enclosures.Value[0].Slots) != 2 {
			t.Errorf("enclosures = %+v after a round that went %+v, want the shelf of two slots", second.Enclosures, second.EnclosuresRound)
		}
	})
}

// The cards are asked about at the same time, so a round takes as long as the slowest card and not as long as all of them added together: with
// 214 phys between two cards that is the difference between fitting in an interval and not.
func TestTheCardsAreAskedAtTheSameTime(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.Delay(polltest.Of(polltest.Links, inside), 10*time.Second)
		kernel.Delay(polltest.Of(polltest.Links, outside), 4*time.Second)
		p := newPoller(t, kernel)

		start := time.Now()
		p.Poll(t.Context(), poll.RoundLinks)

		if ended := time.Now(); !ended.Equal(start.Add(10 * time.Second)) {
			t.Errorf("the round took %s, want the 10s of the slower card", ended.Sub(start))
		}
		if a, b := card(t, p, inside).LinksRound.Took, card(t, p, outside).LinksRound.Took; a != 10*time.Second || b != 4*time.Second {
			t.Errorf("the cards took %s and %s, want each its own 10s and 4s", a, b)
		}
	})
}

// One card failing must not cost the other its answers, nor the failing one the answers it last had.
func TestACardThatFailsDoesNotStopTheOther(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)

		p.Poll(t.Context(), poll.RoundLinks)
		first := time.Now()

		time.Sleep(time.Minute)
		kernel.Fail(polltest.Of(polltest.Links, inside), errReset)
		p.Poll(t.Context(), poll.RoundLinks)

		failed := card(t, p, inside)
		if r := failed.LinksRound; r.OK || r.Failures != 1 || !r.LastSuccess.Equal(first) {
			t.Errorf("the failing card's round = %+v, want one failure and the first round as its last success", r)
		}
		if !failed.Links.Known() || !failed.Links.At.Equal(first) || len(failed.Links.Value.Phys) != 1 {
			t.Errorf("the failing card's links = %+v, want the ones from the first round", failed.Links)
		}
		// the kernel still lists the card and says it is in service, which is all up means
		if !failed.Up() {
			t.Error("a card whose links could not be read is down, though the kernel has it in service")
		}

		if other := card(t, p, outside); !other.LinksRound.OK || other.LinksRound.Failures != 0 || !other.Links.At.After(first) {
			t.Errorf("the other card's round = %+v, want it answered afresh", other.LinksRound)
		}

		kernel.Fail(polltest.Of(polltest.Links, inside), nil)
		p.Poll(t.Context(), poll.RoundLinks)
		if r := card(t, p, inside).LinksRound; !r.OK || r.Failures != 1 {
			t.Errorf("links round = %+v, want it answering again with the one failure still counted", r)
		}
	})
}

// What hangs off a card is kept even when part of it would not be read: one phy that will not give its error counts must not cost the other
// phys theirs. The round still counts as failed, so the gap shows.
func TestLinksPartlyReadAreKept(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)

		time.Sleep(time.Minute)
		kernel.LinksAnswer[inside] = poll.Links{Phys: make([]poll.Phy, 3), Unread: errReset}
		p.Poll(t.Context(), poll.RoundLinks)

		c := card(t, p, inside)
		if len(c.Links.Value.Phys) != 3 || !c.Links.At.Equal(time.Now()) {
			t.Errorf("links = %+v, want the three phys read this round", c.Links)
		}
		if r := c.LinksRound; r.OK || r.Failures != 1 {
			t.Errorf("links round = %+v, want it failed for what could not be read", r)
		}
	})
}

// A card the kernel stops listing stays known and is reported as down, since a card that falls off the bus is the thing most worth seeing. It is
// not asked about, and its rounds count as failed until it is back.
func TestACardThatGoesAway(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)

		kernel.Have(polltest.Card(inside))
		p.Poll(t.Context(), poll.RoundLinks)

		gone := card(t, p, outside)
		if gone.Present || gone.Up() || gone.LinksRound.OK || gone.LinksRound.Failures != 1 {
			t.Errorf("the card that went = present %t, up %t, round %+v; want it known, down and failed once", gone.Present, gone.Up(), gone.LinksRound)
		}
		if kernel.Asked(polltest.Of(polltest.Links, outside)) != 1 {
			t.Errorf("a card that is gone was asked about %d times, want only the once it was there", kernel.Asked(polltest.Of(polltest.Links, outside)))
		}
		if !card(t, p, inside).Up() {
			t.Error("the card that stayed is down")
		}

		kernel.Have(polltest.Card(inside), polltest.Card(outside))
		p.Poll(t.Context(), poll.RoundLinks)
		if back := card(t, p, outside); !back.Up() || !back.LinksRound.OK || back.LinksRound.Failures != 1 {
			t.Errorf("the card that came back = up %t, round %+v; want it up with the one failure still counted", back.Up(), back.LinksRound)
		}
	})
}

// Up is what the kernel says of the card and not how the reading went: a card it is resetting is down while its links are still read.
func TestUpIsWhetherTheKernelHasTheCardInService(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)

		if len(p.State().Cards) != 0 {
			t.Error("a poller that has asked nothing knows of a card")
		}

		kernel.Have(poll.HBA{Name: inside}, polltest.Card(outside))
		p.Poll(t.Context(), poll.RoundLinks)
		if c := card(t, p, inside); c.Up() || !c.LinksRound.OK {
			t.Errorf("a card being reset = up %t, round %+v; want it down with its round answered", c.Up(), c.LinksRound)
		}
		if !card(t, p, outside).Up() {
			t.Error("the card in service is down")
		}
	})
}

// When the kernel will not say which cards it has, every card known of has failed the round, and none is asked about.
func TestTheKernelNotSayingWhichCardsItHas(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)

		kernel.Fail(polltest.HBAs, errReset)
		p.Poll(t.Context(), poll.RoundLinks)
		if len(p.State().Cards) != 0 {
			t.Error("a kernel that never listed a card left the poller knowing of one")
		}

		kernel.Fail(polltest.HBAs, nil)
		p.Poll(t.Context(), poll.RoundLinks)
		kernel.Fail(polltest.HBAs, errReset)
		p.Poll(t.Context(), poll.RoundLinks)

		for _, name := range []string{inside, outside} {
			if r := card(t, p, name).LinksRound; r.OK || r.Failures != 1 {
				t.Errorf("%s: links round = %+v, want it failed once", name, r)
			}
		}
		if kernel.Asked(polltest.Links) != 2 {
			t.Errorf("the cards were asked about %d times, want only the two of the round that listed them", kernel.Asked(polltest.Links))
		}
	})
}

// A machine with no card is not a failure: there is nothing to report, and a card that turns up later is picked up at the next round.
func TestAMachineWithNoCard(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := &polltest.Kernel{}
		p := newPoller(t, kernel)

		p.Poll(t.Context(), poll.RoundLinks)
		p.Poll(t.Context(), poll.RoundEnclosures)
		if len(p.State().Cards) != 0 {
			t.Errorf("cards = %+v, want none", p.State().Cards)
		}

		kernel.Have(polltest.Card(inside))
		p.Poll(t.Context(), poll.RoundLinks)
		if !card(t, p, inside).Up() {
			t.Error("a card that turned up is not up")
		}
	})
}

// A card that has stopped answering is served from its last answers for three intervals and no longer: after that they are no longer worth
// showing as current.
func TestAnAnswerIsDroppedOnceStale(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)
		p.Poll(t.Context(), poll.RoundEnclosures)

		time.Sleep(3 * every.Links)
		if c := card(t, p, inside); !c.Links.Known() || !c.HBA.Known() {
			t.Errorf("after three intervals: links known %t, card known %t; want the last answers still served", c.Links.Known(), c.HBA.Known())
		}

		time.Sleep(time.Second)
		if c := card(t, p, inside); c.Links.Known() || c.HBA.Known() || c.Up() {
			t.Error("the links round's answers outlived three intervals")
		}
		if !card(t, p, outside).Enclosures.Known() {
			t.Error("the enclosures round's answers were dropped on the links round's clock")
		}

		time.Sleep(3 * every.Enclosures)
		if card(t, p, outside).Enclosures.Known() {
			t.Error("the enclosures round's answers outlived three intervals")
		}
	})
}

// Run asks at once, so the page has something on it as soon as it is served, and then on each round's own interval until it is told to stop.
func TestRunPollsOnBothSchedules(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := &polltest.Kernel{Cards: []poll.HBA{polltest.Card(inside)}}
		p := newPoller(t, kernel)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			p.Run(ctx)
			close(done)
		}()

		synctest.Wait()
		if kernel.Asked(polltest.Links) != 1 || kernel.Asked(polltest.Enclosures) != 1 || kernel.Asked(polltest.Sensors) != 1 {
			t.Errorf("on start: links asked %d times, enclosures %d and sensors %d, want once each", kernel.Asked(polltest.Links), kernel.Asked(polltest.Enclosures), kernel.Asked(polltest.Sensors))
		}

		// five minutes of a round every minute is 5 more, and one more of the round every five minutes
		time.Sleep(every.Enclosures + time.Second)
		synctest.Wait()
		if kernel.Asked(polltest.Links) != 6 || kernel.Asked(polltest.Enclosures) != 2 || kernel.Asked(polltest.Sensors) != 6 {
			t.Errorf("after five minutes: links asked %d times, enclosures %d and sensors %d, want 6, 2 and 6", kernel.Asked(polltest.Links), kernel.Asked(polltest.Enclosures), kernel.Asked(polltest.Sensors))
		}

		cancel()
		<-done
	})
}

// A round is given until the next is due and no longer, so a card that hangs has failed the round rather than held every later one up behind it.
func TestARoundIsGivenItsInterval(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.Delay(polltest.Of(polltest.Links, inside), time.Hour)
		p := newPoller(t, kernel)

		p.Poll(t.Context(), poll.RoundLinks)

		if r := card(t, p, inside).LinksRound; !r.Ran || r.OK || r.Took != every.Links || r.Failures != 1 {
			t.Errorf("links round = %+v, want one that failed after its minute", r)
		}
		if r := card(t, p, outside).LinksRound; !r.OK {
			t.Errorf("the other card's round = %+v, want it answered", r)
		}
	})
}

// A round cut short because the program is stopping says nothing about the cards, and is not counted against them.
func TestStoppingIsNotAFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.Delay(polltest.Links, 30*time.Second)
		p := newPoller(t, kernel)

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		p.Poll(ctx, poll.RoundLinks)

		if r := card(t, p, inside).LinksRound; r.Ran || r.Failures != 0 {
			t.Errorf("links round = %+v, want nothing recorded for a round that was stopped", r)
		}
	})
}

// An interval of nothing would poll without pause.
func TestNewRefusesAnIntervalOfNothing(t *testing.T) {
	t.Parallel()

	for _, bad := range []poll.Intervals{{}, {Links: time.Second, Enclosures: time.Second}, {Links: time.Second, Sensors: time.Second}, {Enclosures: time.Second, Sensors: time.Second}, {Links: -time.Second, Enclosures: time.Second, Sensors: time.Second}} {
		if _, err := poll.New(&polltest.Kernel{}, bad, nil); err == nil {
			t.Errorf("New accepted %+v", bad)
		}
	}
}

// What the hardware measures of itself is a round of its own. What was read is kept, and what would not answer makes the round a failed one
// without costing what did answer.
func TestTheSensorsRound(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.SensorsAnswer = map[string]poll.Sensors{
			inside:  {Temperatures: []poll.Temperature{{Sensor: poll.SensorChip, Celsius: 53}}, Enclosures: []poll.EnclosureSensors{{Name: "1:0:24:0", Elements: make([]poll.Element, 3)}}},
			outside: {Temperatures: []poll.Temperature{{Sensor: poll.SensorChip, Celsius: 48}}, Unread: errReset},
		}
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)
		p.Poll(t.Context(), poll.RoundSensors)

		first := card(t, p, inside)
		if r := first.SensorsRound; !r.Ran || !r.OK || r.Failures != 0 {
			t.Errorf("the first card's sensors round = %+v, want it answered", r)
		}
		if s := first.Sensors; !s.Known() || len(s.Value.Temperatures) != 1 || len(s.Value.Enclosures) != 1 || len(s.Value.Enclosures[0].Elements) != 3 {
			t.Errorf("the first card's sensors = %+v, want its temperature and its enclosure", s)
		}

		second := card(t, p, outside)
		if r := second.SensorsRound; !r.Ran || r.OK || r.Failures != 1 {
			t.Errorf("the second card's sensors round = %+v, want it failed for what would not answer", r)
		}
		if s := second.Sensors; !s.Known() || len(s.Value.Temperatures) != 1 || s.Value.Temperatures[0].Celsius != 48 {
			t.Errorf("the second card's sensors = %+v, want the temperature that was read kept", s)
		}

		// the rounds of what the kernel keeps are untouched by it
		if r := second.LinksRound; !r.OK || r.Failures != 0 {
			t.Errorf("the second card's links round = %+v, want it untouched by the sensors", r)
		}

		time.Sleep(3*every.Sensors + time.Second)
		if card(t, p, inside).Sensors.Known() {
			t.Error("the sensors round's answers outlived three intervals")
		}
	})
}

// A program with no leave to open the devices has nothing it can ask. That is how it is run, not a poll that failed: the round is not counted as
// run at all, so nothing on the page says the sensors are being read when they are not, and nothing says they are failing.
func TestSensorsThereIsNoLeaveToRead(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.SensorsAnswer = map[string]poll.Sensors{inside: {Unavailable: "the card's temperature: opening /dev/mpt3ctl: permission denied"}}
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)

		for range 3 {
			p.Poll(t.Context(), poll.RoundSensors)
		}

		for _, name := range []string{inside, outside} {
			c := card(t, p, name)
			if r := c.SensorsRound; r.Ran || r.Failures != 0 || c.Sensors.Known() {
				t.Errorf("%s: sensors round = %+v with an answer kept %t; want nothing recorded for a round with nothing to ask", name, r, c.Sensors.Known())
			}
		}

		// given leave to read part of it, that part is read and the round counts
		kernel.SensorsAnswer[inside] = poll.Sensors{Enclosures: []poll.EnclosureSensors{{Name: "1:0:24:0"}}, Unavailable: "the card's temperature: opening /dev/mpt3ctl: permission denied"}
		p.Poll(t.Context(), poll.RoundSensors)
		if c := card(t, p, inside); !c.SensorsRound.OK || len(c.Sensors.Value.Enclosures) != 1 {
			t.Errorf("with the enclosures readable: round %+v and %d enclosures, want it answered with the one", c.SensorsRound, len(c.Sensors.Value.Enclosures))
		}
	})
}

// names is what each card the poller knows of goes by on the page, by the kernel's name for it.
func names(p *poll.Poller) map[string]string {
	cards, out := p.State().Cards, map[string]string{}
	for i := range cards {
		out[cards[i].Name] = cards[i].Called
	}

	return out
}

// A card goes by its model, less the word that says only that it is a card, since the kernel's name for it reads like a machine's and can change at
// a reboot. One that does not say what it is goes by the kernel's name all the same. Two of one model would be one card on the page, so each
// then has the kernel's number for it added.
func TestWhatACardGoesBy(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		kernel.Cards = append(kernel.Cards, poll.HBA{Name: "host9", Model: " SAS 9300 8e ", Running: true}, polltest.Card("host10"))
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)

		want := map[string]string{inside: "9500-16i", outside: "9500-16e", "host9": "SAS-9300-8e", "host10": "host10"}
		if got := names(p); !maps.Equal(got, want) {
			t.Errorf("the cards go by %v, want %v", got, want)
		}

		// a second card of the first one's model turns up
		kernel.Have(append(kernel.Cards, poll.HBA{Name: "host12", Model: "HBA 9500-16i", Running: true})...)
		p.Poll(t.Context(), poll.RoundLinks)
		if got := names(p); got[inside] != "9500-16i-1" || got["host12"] != "9500-16i-12" || got[outside] != "9500-16e" {
			t.Errorf("with two cards of one model they go by %v, want each with the kernel's number for it and the third as it was", got)
		}
	})
}

// A name can be chosen for a card: by the name it would have had, or by its SAS address however that is written, which is what tells two cards of
// one model apart and comes first when both are given. Cards given one name between them are still two cards. A name chosen for a card that is
// not there changes nothing.
func TestACardGoesByTheNameChosenForIt(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := &polltest.Kernel{Cards: []poll.HBA{
			{Name: inside, Model: "HBA 9500-16i", SASAddress: "500062b000000001", Running: true},
			{Name: outside, Model: "HBA 9500-16e", SASAddress: "500062b000000002", Running: true},
			{Name: "host9", Model: "HBA 9500-16e", SASAddress: "500062b000000003", Running: true},
		}}
		p, err := poll.New(kernel, every, poll.Names{"9500-16i": "inside", "9500-16e": "shelf", "0x500062B000000003": "jbod", "9400-8i": "spare"})
		if err != nil {
			t.Fatal(err)
		}
		p.Poll(t.Context(), poll.RoundLinks)

		want := map[string]string{inside: "inside", outside: "shelf", "host9": "jbod"}
		if got := names(p); !maps.Equal(got, want) {
			t.Errorf("the cards go by %v, want %v", got, want)
		}

		same, err := poll.New(kernel, every, poll.Names{"9500-16e": "shelf"})
		if err != nil {
			t.Fatal(err)
		}
		same.Poll(t.Context(), poll.RoundLinks)
		if got := names(same); got[outside] != "shelf-8" || got["host9"] != "shelf-9" || got[inside] != "9500-16i" {
			t.Errorf("two cards given one name go by %v, want each with the kernel's number for it", got)
		}
	})
}

// A card that has gone keeps the name it went by, long after what it last said of itself has gone stale: it is then that its readings matter,
// and they must not move to another name.
func TestACardThatHasGoneKeepsItsName(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kernel := twoCards()
		p := newPoller(t, kernel)
		p.Poll(t.Context(), poll.RoundLinks)

		kernel.Have(kernel.Cards[0])
		time.Sleep(time.Hour)
		p.Poll(t.Context(), poll.RoundLinks)

		gone := card(t, p, outside)
		if gone.HBA.Known() || gone.Present {
			t.Fatalf("an hour after it went the card is present %t and what it said of itself is known %t; want neither", gone.Present, gone.HBA.Known())
		}
		if gone.Called != "9500-16e" {
			t.Errorf("the card that went now goes by %q, want the 9500-16e it went by", gone.Called)
		}
	})
}
