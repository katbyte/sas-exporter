# sas-exporter

[![GitHub release](https://img.shields.io/github/v/release/katbyte/sas-exporter?color=blueviolet)](https://github.com/katbyte/sas-exporter/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/sas-exporter?color=00ADD8)](https://github.com/katbyte/sas-exporter/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/sas-exporter?color=blue)](https://github.com/katbyte/sas-exporter/blob/main/LICENSE)
![build](https://github.com/katbyte/sas-exporter/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/sas-exporter/actions/workflows/pr-tests.yaml/badge.svg)
![acceptance](https://github.com/katbyte/sas-exporter/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/sas-exporter/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/sas-exporter/badges/coverage.json)](https://github.com/katbyte/sas-exporter/actions/workflows/coverage.yaml)

Reports the health of a Linux machine's SAS cards, cables, backplanes and disks as a [Prometheus](https://prometheus.io) metrics page.

It is there to show a bad cable, a slow link, a failing slot or a stopped fan before the filesystem notices.

**How it behaves.** It only ever reads. A scrape is answered from memory and never waits on a card. A reading it could not get is left off the page, never shown as zero.

## Install

```bash
docker pull ghcr.io/katbyte/sas-exporter:latest     # or
go install github.com/katbyte/sas-exporter@latest   # or
brew install katbyte/tap/sas-exporter               # Homebrew on Linux
```

or a ready-built program from the [releases](https://github.com/katbyte/sas-exporter/releases/latest). Linux only: it reads the Linux kernel's `/sys`.

## Quick start

Run it on the machine the cards are in.

```bash
sas-exporter print   # print the page once: a check that the cards can be seen
sas-exporter serve   # serve it at :9088/metrics, with a health check at /healthz
```

With Docker, [`docker-compose.yml`](docker-compose.yml) runs the released image:

```bash
docker compose up -d
curl -s http://127.0.0.1:9088/metrics
```

Nothing is mounted and no privilege is given. The image does not run as root.

## What it reports

| Readings | What they say |
|---|---|
| [`sas_card_*`](docs/readings.md#cards) | each card: in service or not, model and firmware, restarts, temperature, and its slot |
| [`sas_expander_*`, `sas_phy_*`](docs/readings.md#lanes) | each lane: what it leads to, link speed, and errors counted |
| [`sas_device_*`](docs/readings.md#disks) | each disk: serial, slot, state, and commands that failed or timed out |
| [`sas_enclosure_*`](docs/readings.md#enclosures) | each enclosure: slots, fans, power supplies, temperatures and voltages |
| [`sas_poll_*`](docs/readings.md#polling) | how the program's own polling is going |

Every reading has a `card` label. A card goes by its model, `9500-16i`, or by [a name you choose](docs/readings.md#what-a-card-goes-by).

Most of this needs no root and no setup. Fans, power supplies and temperatures [need root](docs/root.md).

It was written for, and run on, Broadcom HBA 9500 cards with Supermicro backplanes. Other cards the kernel lists as SAS should work but are untested.

## Settings

All are optional. Each can be a flag, an environment variable, or a line in a settings file.

| Variable | Flag | Default | What it sets |
|---|---|---|---|
| `SAS_EXPORTER_LISTEN` | `--listen` | `:9088` | the address the page is served on |
| `SAS_EXPORTER_LABELS` | `--labels` | none | labels for every reading, as `name=value,name=value` |
| `SAS_EXPORTER_CARD_NAMES` | `--card-names` | none | your own names for cards, as `9500-16e=jbod` |
| `SAS_EXPORTER_SENSORS` | `--sensors` | `all` | what is asked of the hardware: `all`, `none`, `enclosures` or `cards` |
| `SAS_EXPORTER_INTERVALS` | `--intervals` | `links=1m,enclosures=5m,sensors=1m` | how often each part is read: `links` the cards, lanes and disks, `enclosures` the slots, `sensors` the fans and temperatures |
| `SAS_EXPORTER_SYSFS` | `--sysfs` | `/sys` | where the kernel's `/sys` is |
| `SAS_EXPORTER_DEV` | `--dev` | `/dev` | where the device files are |
| `SAS_EXPORTER_LOG` | | `WARN` | log level: `INFO` or `DEBUG` say more |
| `SAS_EXPORTER_CONFIG` | `--config` | | the one settings file to read |

The settings file is `./.sas-exporter` or `~/.sas-exporter`. Its keys are the variable names without `SAS_EXPORTER_`:

```env
LISTEN=:9088
INTERVALS=links=2m,enclosures=10m
```

A flag or an environment variable wins over the file.

## Scraping it

```yaml
scrape_configs:
  - job_name: sas
    scrape_interval: 30s
    static_configs:
      - targets: ['127.0.0.1:9088']
```

A few things to ask of it:

```promql
sas_card_up == 0                                      # a card is gone or being reset
increase(sas_phy_errors_total[1h]) > 0               # a lane counted errors in the last hour
sas_phy_link_state{state=~"failed|disabled"} == 1    # a link that would not come up
sas_device_state{state!="running"} == 1              # a disk the kernel has stopped sending to
sas_enclosure_fan_speed_rpm == 0                     # a fan that has stopped
```

[More queries](docs/queries.md), and the same scrape for Grafana Alloy.

## How this is built

- **Tested against a real machine.** The tests read a saved copy of a real machine's `/sys` and of the pages its enclosures answered with: two cards, 214 lanes, 58 disks, 14 fans.
- **The image is tested as it ships.** Live tests start the Docker image, hand it that saved machine and scrape it. CI fails if they fail.
- **What no test reaches.** A real card or enclosure answering. That part was run by hand on the machine it was written for.
- **Checks CI runs.** Unit tests under the race detector, and the live tests. golangci-lint, CodeQL, govulncheck and a scan of the built program. Linters for the workflows, shell scripts, YAML, markdown and spelling.

## More

- [Every reading and label](docs/readings.md)
- [What needs root, and running it in a container](docs/root.md)
- [More queries](docs/queries.md)
- [Development, tests and bug reports](docs/development.md)
- [Contributing and releases](CONTRIBUTING.md)
