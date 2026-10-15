# Development

```bash
make            # format and build
make test       # the tests, under the race detector
make testacc    # the live tests: the image, as docker runs it
make cover      # the tests with coverage
make check-all  # build, tests, live tests, every linter and the dependency check
make tools      # install the pinned dev tools into .tools/bin
```

Dependencies are vendored. The dev tools are pinned in `.tools/go.mod` and the makefile.

## Where things are

| Path | What it does |
|---|---|
| `lib/sysfs` | asks the kernel, by reading `/sys` |
| `lib/devices` | the only part that opens a device to ask the hardware |
| `lib/ses` | reads what an enclosure answers |
| `lib/mpt` | reads what a card answers |
| `lib/poll` | says what is asked of each card, and what a card is called |
| `lib/readings` | writes the answers as readings |
| `cli` | the commands and settings |
| `acceptance` | the live tests of the image |

The rest is shared with the other exporters, from [go-kt](https://github.com/katbyte/go-kt)'s `prometheus` packages:

- `metrics`: the page, `/metrics`, `/healthz`, the labels, and the list and writer of readings.
- `poller`: asking on a schedule, keeping the last answers, and the `sas_poll_*` readings.
- `exporter`: what `print` and `serve` run.
- `metricstest`: the saved-page check, the naming check, and the live tests' helpers.

## The tests

`make test` needs no SAS card.

- Most tests lay out a small made-up `/sys` of their own: a card, a backplane, a disk in a slot. The builder is `lib/sysfs/sysfstest`.
- The rest read [`nas.txt`](../lib/sysfs/sysfstest/testdata/nas.txt), a real machine's `/sys` as `capture.sh` printed it and `anonymize.sh` rewrote it.
- That machine has two cards, four expanders, 214 phys and 58 disks. The phys its expanders refuse counts for refuse them in the tests too.
- It also holds the pages its four enclosures answered with: 14 fans, 4 power supplies, 10 temperatures and 24 voltages.
- A second real enclosure, of another make, is in [`areca.hex`](../lib/ses/testdata/areca.hex).
- The whole page is kept in [`page.txt`](../lib/readings/testdata/page.txt), so a renamed reading shows up as a difference. `go test ./lib/readings -update` rewrites it once the change is meant.

## The live tests

`make testacc` starts the image `make docker` builds and scrapes it over HTTP. It needs `docker` and takes a few seconds.

- Handed the real machine's saved `/sys`, the image serves every card, expander, phy, disk and slot that machine has.
- It passes its own health check and does not run as root.
- As that user it cannot ask the hardware, so the page has no fans or temperatures and the log says why, once for each card.
- A card is then taken away. The page shows it down with its readings left off, while the other card carries on.
- Given nothing, it reads the `/sys` docker gives every container, and `print` says there is no card.

## What the tests cannot reach

- **A card itself.** What the kernel says while a cable is pulled is only seen on a machine with cards in it. Those paths are tested against a `/sys` made to look that way, and a kernel made to refuse or to hang.
- **The hardware answering.** What is sent to an enclosure and to a card is checked byte for byte. The check is against what the kernel's own driver and `sg_ses` are written to send.
- Where each part of a request sits is checked against what a C compiler makes of the kernel's own.
- On Linux the tests hand a request to the kernel for a file that is no device, and see it refused.
- No test has a card's own answer to read. That was run by hand.

## When a reading looks wrong

[`capture.sh`](../.scripts/capture.sh) writes down what the kernel says of the machine's SAS cards as text. It only reads, needs no root, and takes about as long as one poll.

```bash
.scripts/capture.sh > capture.txt                       # on the machine with the cards
ssh nas 'sh -s' < .scripts/capture.sh > capture.txt     # or from another, with nothing copied over
.scripts/anonymize.sh < capture.txt > shareable.txt     # before showing it to anyone
```

- Run as root on a machine with `sg_ses`, it also saves the pages each enclosure answers with. Fans, power supplies and temperatures are read from those.
- A capture names every disk by serial number and address.
- [`anonymize.sh`](../.scripts/anonymize.sh) swaps each for a made-up one, the same one each time it appears.
- It swaps the names of power supplies too, since some backplanes name them by serial number.
- Models, firmware versions, speeds and counts stay as they were.
- It leaves the other names an enclosure gives its parts. Look those over before showing anyone a capture of another make of enclosure.
