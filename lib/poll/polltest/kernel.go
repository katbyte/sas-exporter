// Package polltest is a kernel for tests: one that has the cards it is told to, answers what it is told to about each, fails when told to, and
// counts what it was asked.
package polltest

import (
	"context"
	"sync"
	"time"

	"github.com/katbyte/sas-exporter/lib/poll"
)

// The questions a kernel is asked, as Fail, Delay and Asked name them. One of them about one card is the question, a space, and the card's name:
// see Of.
const (
	HBAs       = "hbas"
	Links      = "links"
	Enclosures = "enclosures"
	Sensors    = "sensors"
)

// Of names a question about one card, for Fail, Delay and Asked.
func Of(question, hba string) string {
	return question + " " + hba
}

// Kernel answers each question with the value set for it. The zero value is a machine with no SAS card; set the fields before it is polled.
type Kernel struct {
	// Cards is the cards it has now.
	Cards []poll.HBA
	// LinksAnswer and EnclosuresAnswer are what hangs off each card, by the card's name. A card with none set has nothing on it.
	LinksAnswer      map[string]poll.Links
	EnclosuresAnswer map[string][]poll.Enclosure
	SensorsAnswer    map[string]poll.Sensors

	mu      sync.Mutex
	failing map[string]error
	slow    map[string]time.Duration
	asked   map[string]int
}

// Card is a card in service under that name, for Cards.
func Card(name string) poll.HBA {
	return poll.HBA{Name: name, Running: true}
}

// Fail makes one question fail with err from now on, and every question when question is "". A nil err puts it right again.
func (k *Kernel) Fail(question string, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.failing == nil {
		k.failing = map[string]error{}
	}
	k.failing[question] = err
}

// Delay makes one question take that long to answer from now on, or until the context ends if that is sooner, and every question when question is "".
func (k *Kernel) Delay(question string, d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.slow == nil {
		k.slow = map[string]time.Duration{}
	}
	k.slow[question] = d
}

// Asked is how many times a question has been put: of every card, or of one when it is named with Of.
func (k *Kernel) Asked(question string) int {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.asked[question]
}

// answer counts the question, takes as long as it was told to, and gives the error it was told to fail with. What is set for the question about
// one card comes before what is set for the question, and that before what is set for every question.
func (k *Kernel) answer(ctx context.Context, question, hba string) error {
	names := []string{question, ""}
	if hba != "" {
		names = []string{Of(question, hba), question, ""}
	}

	k.mu.Lock()
	if k.asked == nil {
		k.asked = map[string]int{}
	}
	k.asked[question]++
	if hba != "" {
		k.asked[Of(question, hba)]++
	}

	var err error
	var slow time.Duration
	for _, name := range names {
		if err == nil {
			err = k.failing[name]
		}
		if slow == 0 {
			slow = k.slow[name]
		}
	}
	k.mu.Unlock()

	if slow > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(slow):
		}
	}

	return err
}

func (k *Kernel) HBAs(ctx context.Context) ([]poll.HBA, error) {
	k.mu.Lock()
	cards := append([]poll.HBA(nil), k.Cards...)
	k.mu.Unlock()

	return cards, k.answer(ctx, HBAs, "")
}

func (k *Kernel) Links(ctx context.Context, hba string) (poll.Links, error) {
	k.mu.Lock()
	links := k.LinksAnswer[hba]
	k.mu.Unlock()

	return links, k.answer(ctx, Links, hba)
}

func (k *Kernel) Enclosures(ctx context.Context, hba string) ([]poll.Enclosure, error) {
	k.mu.Lock()
	enclosures := k.EnclosuresAnswer[hba]
	k.mu.Unlock()

	return enclosures, k.answer(ctx, Enclosures, hba)
}

func (k *Kernel) Sensors(ctx context.Context, hba string) (poll.Sensors, error) {
	k.mu.Lock()
	sensors := k.SensorsAnswer[hba]
	k.mu.Unlock()

	return sensors, k.answer(ctx, Sensors, hba)
}

// Have changes the cards the kernel has, while it is being polled.
func (k *Kernel) Have(cards ...poll.HBA) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.Cards = cards
}
