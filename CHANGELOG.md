# Changelog

## 0.3.1 (2026-10-18)

- **Says which build it is when it starts serving**: the first line it prints is the version and the commit it was built from, `sas-exporter v0.3.1 (1a2b3c4)`, and `sas-exporter version` prints the same.
- **The commit on `sas_exporter_build_info` is just the commit** in a build made with `make`, as it already was in a release, not the version and commit run together.

## 0.3.0 (2026-10-18)

- **Leave out any part**, and it is not asked for either: `--skip` (`SAS_EXPORTER_SKIP`) takes a list of `poll`, `cards`, `expanders`, `phys`, `phy-errors`, `link-changes`, `devices`, `enclosures`, `slots`, `card-sensors` and `enclosure-sensors`. Everything is on unless named.
- **Or name the only parts to ask about**: `--metrics` (`SAS_EXPORTER_METRICS`) takes the same names, and `--skip` then leaves parts out of those.
- **`--skip` replaces `--sensors`**, which is gone: `--sensors none` is now `--skip card-sensors,enclosure-sensors`, `--sensors enclosures` is `--skip card-sensors`, and `--sensors cards` is `--skip enclosure-sensors`.
- **Which connector on the card each lane is wired to**, as printed beside it (`C0.0`), on `sas_phy_connector_info` beside the backplanes' own. *Root*.
- **How often each link has dropped and come back**, the card's own and every expander's, as the card counts, and how often anything about each expander has changed. A cable that comes and goes between polls shows here. *Root*.

## 0.2.0 (2026-10-15)

More of what the kernel lists, after checking 0.1.0 against everything it offers on a real machine.

- **How busy each card is**: the commands out with it when it was last asked, beside the most the kernel will have out with it at once.
- **Each card's serial and part number**, the chip it is built on, and the versions of its settings (written as storcli writes them) and of the language it and its driver speak, on `sas_card_info`.
- **Whether a card has slowed itself**, and which power mode it is in. *Root*.
- **Whether each slot has power**, since an enclosure can turn a slot off.
- **The speeds each phy is set to and can do**: the least it is set to offer, and the most and least it can, beside the most it is set to offer that was already there.
- **The chip each expander is built on**, on `sas_expander_info`, and **each backplane's firmware version**, on `sas_enclosure_info`.
- **Parts an enclosure expects to fail**, and whether each part's locate light is on. *Root*.
- **Which backplane connector each lane is wired to**, as the backplane names it, so a lane with errors points at a cable. *Root*.
- **How many commands each disk takes at once**, and how long the kernel waits for one before giving up on it.
- **The limits each voltage and current sensor is held to**, as the share of what it should read that the enclosure gives: 0.15 is 15% above it. The enclosure never says what the sensor should read, so that is left to its name. *Root*.
- `sas_card_info`, `sas_expander_info` and `sas_enclosure_info` have new labels, so each starts a new series once.

## 0.1.0 (2026-10-15)

The first release: a metrics page for the SAS cards in a Linux machine and everything that hangs off them.

- **How each card is doing, as a page Prometheus can read**: whether the kernel has it in service, its model, firmware and driver, how often its firmware has been restarted, and whether its own link to the machine came up as fast and as wide as the card was made for.
- **Every lane**: the speed each link came up at in gigabits a second, whether it came up at all, and the four counts of what has gone wrong on it, so a bad cable or connector shows as a count that keeps growing.
- **Cables told apart from slots**: each lane says whether it leads to a disk, on to a further backplane, or back towards the card.
- **Every disk, by serial number**: what it is, which slot it sits in and which lane its link is on, whether the kernel is still sending to it, and how many commands it was sent, answered, failed or never answered.
- **Every slot of every enclosure**: empty, fine or failed, and whether its fault or locate light is on.
- **Nothing to set up for any of that**: it reads the kernel's `/sys`, so it needs no root, no privileged container and no maker's tool, and runs with no settings at all.
- **Fans, power supplies, temperatures and voltages, when run as root**: everything else an enclosure says of itself, each under the name the enclosure gives it, with what is wrong with a power supply spelt out. Each temperature comes with the limits the enclosure holds it to. These are asked of the enclosure itself, which the kernel only lets root do.
- **Each card's own temperature, when run as root**, asked of the card through its driver. A card that fails to answer is never asked twice, since its driver resets a card that does not answer.
- **Without root nothing breaks**: the page simply lacks those readings and the log says why, once. `--sensors` chooses which of the two are asked at all.
- **One card going away does not stop another being reported**, and a card that stops answering is asked no more that round rather than pushed into a reset.
- **`serve` keeps the page up** for a scraper to read, and `print` shows it once as a check that the cards can be seen.
- **Each card goes by its model**, `9500-16i`, on every one of its readings, and by a name of your choosing when you give it one.
- **Labels of your own on every reading**, to tell one machine from another.
- Ships as a Docker image, a Homebrew formula and ready-built programs, for Linux.
