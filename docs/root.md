# What needs root

Most readings come from the kernel's `/sys`. That needs no root, no privileged container and no maker's tool, and opens no device.

Two things are not kept there. They are asked of the hardware itself, through device files only root can open:

| What | Asked through |
|---|---|
| an enclosure's fans, power supplies, temperatures and voltages, and which connector each lane is wired to | the enclosure's own `/dev/sgN` |
| a card's own temperature, speed and power mode, the connectors its phys are wired to, and how often each link has changed | `/dev/mpt3ctl`, the `mpt3sas` driver's way in to its cards |

## Without root

- The program runs as it would anyway.
- The page lacks those readings.
- The log says why, once for each card.
- Nothing is counted as failed.

`--skip card-sensors` leaves the cards alone, `--skip enclosure-sensors` the enclosures, and the two together ask the hardware nothing. See [leaving parts out](readings.md#leaving-parts-out).

## What is asked

- Both only read.
- An enclosure's device is opened for reading. The kernel then lets nothing through it but commands that read.
- The question put to a card is the one its maker's own tools ask.
- Only cards on the `mpt3sas` driver are asked.
- An enclosure that will not give its limits, or which connector each lane is wired to, is not asked for that again.

## One caution about asking a card

- The driver gives a card ten seconds to answer each question.
- It resets a card that does not, which holds up every disk behind it while it comes back.
- A healthy card answers at once: in under a thousandth of a second on the cards this was written on.
- Each round asks a card about a hundred questions: one for each of its phys and each of its expanders' phys, answered from what the card already knows.
- The first question a card fails to answer is the last it is asked until the program is restarted. The log says so.
- `--skip card-sensors` leaves the cards alone altogether, and `--skip link-changes` only the hundred questions.

## Try it by hand first

```bash
sudo sas-exporter print --skip card-sensors    # the enclosures only
sudo sas-exporter print                        # the cards as well
```

The page then has the fans and temperatures on it. Anything that could not be asked is said in the log beside it.

## In a container

The default container is not root and is given no devices. For these readings it has to run as root and be given the machine's devices.

The simplest way gives it all of them:

```yaml
    user: root
    privileged: true
    volumes:
      - /dev:/host/dev:ro
    environment:
      SAS_EXPORTER_DEV: /host/dev
```

Or give it leave to read the two kinds of device it asks, and no others:

```yaml
    user: root
    device_cgroup_rules:
      - 'c 21:* r'    # /dev/sg*, which enclosures are asked through
      - 'c 10:222 r'  # /dev/mpt3ctl, which cards are asked through
    volumes:
      - /dev:/host/dev:ro
    environment:
      SAS_EXPORTER_DEV: /host/dev
```

- The machine's `/dev` is mounted whole because an enclosure's `sgN` can be another number after a reboot.
- `ls -l /dev/sg0 /dev/mpt3ctl` shows the numbers a rule is written with.
- [`docker-compose.yml`](../docker-compose.yml) has these lines commented out.

## Everything else in a container

- Nothing is mounted and no privilege is given.
- Docker gives every container a read-only `/sys` of its own, and the machine's SAS cards are in it.
- `--sysfs` says where `/sys` is, for a container given the host's somewhere else.
- The example publishes the port on `127.0.0.1` only, since the page needs no password. Drop the address when it is scraped from another machine.
