# Readings

Every reading starts `sas_` and has a `card` label saying which card it hangs off. The page holds nothing else: none of the Go runtime's or the process's own readings.

Readings marked *root* are only there when the program [runs as root](root.md).

## Cards

| Reading | Labels | What it says |
|---|---|---|
| `sas_card_up` | | 1 while the kernel has the card in service, 0 while it is being reset or has gone |
| `sas_card_info` | `model`, `firmware`, `bios`, `driver`, `driver_version`, `sas_address`, `scsi_host` | always 1; what the card is and what it runs |
| `sas_card_resets_total` | | times the card's firmware has restarted since its driver loaded |
| `sas_card_temperature_celsius` | `sensor` | the card's own temperature: `chip` or `board`. *Root* |
| `sas_card_pcie_link_speed_gigatransfers_per_second` | | speed of the card's own slot: 16 is PCIe 4.0, 8 is 3.0 |
| `sas_card_pcie_maximum_link_speed_gigatransfers_per_second` | | the fastest the card could run |
| `sas_card_pcie_link_width` | | lanes the slot gave the card |
| `sas_card_pcie_maximum_link_width` | | lanes the card could use |

- A slot slower or narrower than the maximum is a card in the wrong slot, or not seated.
- An HBA 9500 gives only the `chip` temperature.

### What a card goes by

A card goes by its model, without the leading `HBA`: an `HBA 9500-16i` is `card="9500-16i"`. That name survives a reboot and says which card it is.

The kernel's own name for the card, `host1`, reads like a machine's and can change. It is on `sas_card_info` as `scsi_host`.

`--card-names` gives a card a name of your choosing:

```bash
sas-exporter serve --card-names 9500-16i=inside,9500-16e=jbod
```

- Name a card by what it goes by without one, or by its SAS address.
- The SAS address is what tells two cards of one model apart.
- Without names, two cards of one model each get the kernel's number added: `9500-16i-1` and `9500-16i-8`.
- A card that does not say its model goes by the kernel's name.

## Lanes

A phy is one end of one lane. A card has one for each lane it can drive, and so does an expander. An expander is the chip on a backplane that fans a card's lanes out to the slots.

`expander` says which expander a phy is on. It is empty for one of the card's own.

| Reading | Labels | What it says |
|---|---|---|
| `sas_expander_info` | `expander`, `vendor`, `product`, `revision`, `sas_address` | always 1; one for each expander |
| `sas_phy_info` | `expander`, `phy`, `attached` | always 1; what is on the other end of the link |
| `sas_phy_link_state` | `expander`, `phy`, `state` | always 1; how the link is doing |
| `sas_phy_link_rate_gigabits_per_second` | `expander`, `phy` | the speed the link came up at: 12, 6, 3 or 1.5. 0 with no link |
| `sas_phy_maximum_link_rate_gigabits_per_second` | `expander`, `phy` | the most the phy is set to offer |
| `sas_phy_errors_total` | `expander`, `phy`, `error` | what the phy has counted going wrong on its link |

`state` is `up`, `down`, `disabled`, `failed`, `spinup_hold` or `other`. `failed` is two ends that could not agree a speed. `spinup_hold` is a disk waiting to be told to spin up.

### What is on the other end

`attached` is what tells a bad cable from a bad slot.

| `attached` | What it is |
|---|---|
| `device` | a disk, or an enclosure's own processor |
| `expander` | a further expander, away from the card |
| `upstream` | the way back towards the card |
| `none` | nothing the kernel lists: an empty slot, or a phy the expander uses itself |

A cable between a card and a backplane is the card's phys with `expander` on them and the backplane's with `upstream`.

The kernel lists what is below a phy and never what is above it, so `upstream` is worked out. It is an expander's lowest-numbered phys that have a link and nothing below them, as many as the port leading to that expander is wide. An expander's phys for its own use are numbered after the ones with a connector, which keeps them out of the count.

### The error counts

| `error` | What was counted |
|---|---|
| `invalid_dword` | words that arrived garbled |
| `running_disparity` | words that arrived unbalanced, another sign of a noisy lane |
| `loss_of_dword_sync` | times the phy lost its place and had to find it again |
| `phy_reset_problem` | times the link failed to come back after a reset |

- A handful from when a link first came up is ordinary.
- A count that keeps growing is a cable, a connector or a slot going bad.
- The counts are kept in the card or the expander. They start again from 0 when it is reset or loses power.
- Each count is on the page at 0 until it counts something.
- A phy that keeps no counts, as an expander's unwired ones do not, has no reading of them.

## Disks

| Reading | Labels | What it says |
|---|---|---|
| `sas_device_info` | `serial`, `disk`, `wwn`, `vendor`, `model`, `revision`, `sas_address`, `expander`, `phy`, `enclosure`, `slot` | always 1; what the disk is, its slot and its lane |
| `sas_device_state` | `serial`, `disk`, `state` | always 1; `running`, or `blocked`, `offline` and the like |
| `sas_device_commands_total` | `serial`, `disk` | commands the kernel has sent the disk |
| `sas_device_commands_completed_total` | `serial`, `disk` | commands the disk has answered, well or badly |
| `sas_device_command_errors_total` | `serial`, `disk` | commands answered with anything but success |
| `sas_device_command_timeouts_total` | `serial`, `disk` | commands the disk never answered |

- A disk goes by its `serial`. That is what joins it to other tools, such as [smartctl_exporter](https://github.com/prometheus-community/smartctl_exporter) for its SMART data.
- Nothing of that kind is repeated here. `sas_device_info` ties a serial number to a slot and a lane.
- Command errors climb slowly on a healthy disk. A tool that reads SMART data asks questions a disk may decline, and each counts.
- So watch for one disk climbing faster than its neighbours.
- Timeouts have no such noise. Any at all is worth a look.
- Fewer commands answered than sent, for long, is a command stuck with the disk.

## Enclosures

| Reading | Labels | What it says |
|---|---|---|
| `sas_enclosure_info` | `enclosure`, `vendor`, `product`, `revision`, `id`, `expander` | always 1; one for each enclosure |
| `sas_enclosure_slots` | `enclosure` | how many slots it has |
| `sas_enclosure_slot_status` | `enclosure`, `slot`, `status` | always 1; `ok`, `not_installed` for an empty slot, or a fault |
| `sas_enclosure_slot_fault` | `enclosure`, `slot` | 1 when the slot's fault light is asked for, or a fault is sensed |
| `sas_enclosure_slot_locate` | `enclosure`, `slot` | 1 when the slot's locate light is on |
| `sas_enclosure_element_status` | `enclosure`, `type`, `element`, `status` | always 1; each part that is not a slot. *Root* |
| `sas_enclosure_element_fault` | `enclosure`, `type`, `element` | 1 when the enclosure reports the part as failed. *Root* |
| `sas_enclosure_fan_speed_rpm` | `enclosure`, `element` | how fast a fan is turning. *Root* |
| `sas_enclosure_temperature_celsius` | `enclosure`, `element` | what a temperature sensor reads. *Root* |
| `sas_enclosure_temperature_limit_celsius` | `enclosure`, `element`, `limit` | a limit the enclosure holds that sensor to. *Root* |
| `sas_enclosure_voltage_volts` | `enclosure`, `element` | what a voltage sensor reads. *Root* |
| `sas_enclosure_current_amperes` | `enclosure`, `element` | what a current sensor reads. *Root* |
| `sas_enclosure_power_supply_problem` | `enclosure`, `element`, `problem` | 1 when a power supply is in that trouble. *Root* |
| `sas_enclosure_condition` | `enclosure`, `condition` | the enclosure's own summary of itself. *Root* |

`status` is `ok`, `not_installed`, `critical`, `non_critical`, `unrecoverable`, `unavailable`, `unknown`, `unsupported` or `no_access_allowed`.

### What an enclosure measures

An enclosure keeps a list of everything in it. The slots are the first readings above. Everything else is a `sas_enclosure_element_status`.

- `element` is what the enclosure calls the part: `FAN01`, `ChipDie`, whatever its maker chose.
- A part with no name goes by its number in the list.
- When two of a kind share a name, the second has its number added.

| `type` | What it is | What else is read of it |
|---|---|---|
| `cooling` | a fan | its speed |
| `power_supply` | a power supply | its problems |
| `temperature_sensor` | a temperature sensor | its temperature and limits |
| `voltage_sensor` | a voltage sensor | its voltage |
| `current_sensor` | a current sensor | its current |
| `sas_expander`, `sas_connector`, `enclosure` and the like | the enclosure's other parts | nothing more |

- A part that is not fitted has a status and nothing measured. A stopped fan reads 0; a missing fan has no speed.
- A backplane only knows the fans and power supplies plugged into it, not the ones on the motherboard.
- A Supermicro backplane names a power supply by its serial number. A swapped supply is a new `element`.

### Limits

`limit` is `high_critical`, `high_warning`, `low_warning` or `low_critical`.

- An enclosure holds each temperature sensor to limits of its own. A backplane's chip may be allowed 100 degrees where the board around it is allowed 46.
- A limit the enclosure does not set is left off.
- An enclosure that does not give limits has none on the page.
- Voltage limits are not reported. The enclosure gives them as a share of what the sensor should read, and does not say what that is.

### Power supply problems

| `problem` | What it is |
|---|---|
| `ac_fail` | it has lost its mains |
| `dc_fail` | it is not giving power |
| `dc_overvoltage`, `dc_undervoltage`, `dc_overcurrent` | what it gives is out of range |
| `overtemperature_fail`, `temperature_warning` | it is too hot, or getting there |
| `off` | it is switched off |

### The enclosure's own summary

`condition` is `info`, `non_critical`, `critical` or `unrecoverable`.

It is the enclosure's own word on itself, not worked out from its parts. Some say `critical` with every part reading `ok`: three of the four backplanes this was written on do. Watch it for a change, not for being set.

## Names that move, and addresses that do not

`expander`, `enclosure` and `disk` are the kernel's names for a thing today: `expander-1:0`, `1:0:24:0`, `sdb`.

- They are the names `lsscsi` and `/sys` use, so a reading can be taken straight to the machine.
- They can change at a reboot or when a cable is moved.
- Each info reading also carries what does not change: the expander's `sas_address`, the enclosure's `id`, the disk's `serial`.
- The number those names begin with is the kernel's number for the card. `expander-1:0` and `1:0:24:0` are behind `host1`.

## Polling

| Reading | Labels | What it says |
|---|---|---|
| `sas_poll_interval_seconds` | `poll` | how often the card is asked, once it has been |
| `sas_poll_duration_seconds` | `poll` | how long the last poll of the card took |
| `sas_poll_last_success_timestamp_seconds` | `poll` | when a poll was last answered in full |
| `sas_poll_failures_total` | `poll` | polls that have failed since the program started |
| `sas_exporter_build_info` | `version`, `commit`, `goversion` | always 1; the version that wrote the page. No `card` label |

`poll` is `links`, `enclosures` or `sensors`.

The kernel is asked on the program's own schedule, and the page holds the last answers. Reading error counts and slots makes the kernel ask the hardware there and then, which is why neither is done for every scrape.

### When a card stops answering

- `sas_card_up` goes to 0 at the next poll when a card is being reset or is no longer listed. Another card's readings carry on.
- A card that has gone stays on the page as down until the program is restarted.
- Its last readings stay for three polls, to ride out a blip, and are then left off.
- Three polls is 3 minutes for lanes and sensors and 15 for slots, at the default intervals.
- A reading that has never been answered is absent, not zero.
- A card that has stopped answering costs ten seconds for each error count asked. The kernel may reset the card each time.
- So the first count that takes seconds is the last one asked of that card that round. The rest of its phys are on the page without counts, and the poll counts as failed.
