# Readings

Every reading starts `sas_` and has a `card` label saying which card it hangs off. The page holds nothing else: none of the Go runtime's or the process's own readings.

Readings marked *root* are only there when the program [runs as root](root.md).

## Cards

| Reading | Labels | What it says |
|---|---|---|
| `sas_card_up` | | 1 while the kernel has the card in service, 0 while it is being reset or has gone |
| `sas_card_info` | `model`, `firmware`, `bios`, `driver`, `driver_version`, `sas_address`, `scsi_host`, `serial`, `assembly`, `chip`, `nvdata`, `nvdata_default`, `mpi` | always 1; what the card is and what it runs |
| `sas_card_resets_total` | | times the card's firmware has restarted since its driver loaded |
| `sas_card_commands_in_flight` | | commands sent to the card and not yet back, when it was last asked |
| `sas_card_commands_in_flight_limit` | | the most the kernel will have out with the card at once |
| `sas_card_temperature_celsius` | `sensor` | the card's own temperature: `chip` or `board`. *Root* |
| `sas_card_speed_ratio` | | how fast the card's processor runs, as a share of full speed. *Root* |
| `sas_card_power_mode` | `mode` | always 1; `full`, `reduced` or `standby`. *Root* |
| `sas_card_pcie_link_speed_gigatransfers_per_second` | | speed of the card's own slot: 16 is PCIe 4.0, 8 is 3.0 |
| `sas_card_pcie_maximum_link_speed_gigatransfers_per_second` | | the fastest the card could run |
| `sas_card_pcie_link_width` | | lanes the slot gave the card |
| `sas_card_pcie_maximum_link_width` | | lanes the card could use |

- A slot slower or narrower than the maximum is a card in the wrong slot, or not seated.
- An HBA 9500 gives only the `chip` temperature, which storcli calls the ROC temperature.
- A speed below 1 is a card that has slowed itself, to keep cool or to save power.
- `nvdata` is the version of the settings the card keeps, `nvdata_default` of the ones it came with, written as storcli writes them: `32.01.00.11`. They differ once the card's settings have been changed.
- Commands in flight is a glimpse once a minute, not an average: it shows a card that stays busy, not a burst.
- A disk's queue fills long before a card's. A 9500 takes 4,336 commands at once, more than 36 disks at 64 each can send it, so `sas_device_queue_depth` says more.

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
| `sas_expander_info` | `expander`, `vendor`, `product`, `revision`, `sas_address`, `chip_vendor`, `chip`, `chip_revision` | always 1; one for each expander, and the chip it is built on |
| `sas_phy_info` | `expander`, `phy`, `attached` | always 1; what is on the other end of the link |
| `sas_phy_link_state` | `expander`, `phy`, `state` | always 1; how the link is doing |
| `sas_phy_link_rate_gigabits_per_second` | `expander`, `phy` | the speed the link came up at: 12, 6, 3 or 1.5. 0 with no link |
| `sas_phy_maximum_link_rate_gigabits_per_second` | `expander`, `phy` | the most the phy is set to offer |
| `sas_phy_minimum_link_rate_gigabits_per_second` | `expander`, `phy` | the least the phy is set to offer |
| `sas_phy_hardware_maximum_link_rate_gigabits_per_second` | `expander`, `phy` | the most the phy can do |
| `sas_phy_hardware_minimum_link_rate_gigabits_per_second` | `expander`, `phy` | the least the phy can do |
| `sas_phy_errors_total` | `expander`, `phy`, `error` | what the phy has counted going wrong on its link |
| `sas_phy_connector_info` | `expander`, `phy`, `enclosure`, `connector` | always 1; the connector the phy is wired to, on a backplane or on the card. *Root* |
| `sas_phy_link_changes_total` | `expander`, `phy` | times the link has dropped, come back or changed speed. *Root* |
| `sas_expander_changes_total` | `expander` | times anything about the expander has changed. *Root* |

`state` is `up`, `down`, `disabled`, `failed`, `spinup_hold` or `other`. `failed` is two ends that could not agree a speed. `spinup_hold` is a disk waiting to be told to spin up.

A phy set to offer less than it can do has been held back on purpose. A phy that does not say what speeds it can do has none of those four: a card's spare phys past its connectors, and an expander's unwired ones.

### What is on the other end

`attached` is what tells a bad cable from a bad slot.

| `attached` | What it is |
|---|---|
| `device` | a disk, or an enclosure's own processor |
| `expander` | a further expander, away from the card |
| `upstream` | the way back towards the card |
| `none` | nothing the kernel lists: an empty slot, or a phy the expander uses itself |

A cable between a card and a backplane is the card's phys with `expander` on them and the backplane's with `upstream`.

`sas_phy_connector_info` names the connector each phy is wired to, which says which cable a lane with errors is in:

- On a backplane, as the backplane names it: `ConnectorA` for a cable's, `Drive Connector 05` for a slot's.
- On the card, as printed beside it, `C0.0`, with `expander` and `enclosure` empty.
- Only a backplane or a card that says so has it.

`sas_phy_link_changes_total` is how many times the card has seen a link drop, come back, or come back at another speed. It catches a cable that comes and goes between polls, which the link's state, read once a minute, can miss.

- A handful from when the machine started is ordinary.
- One that keeps climbing is a connection coming and going, even while its error counts stay still.
- The card keeps eight bits of each count, so it goes back to 0 after 255. A rate or an increase reads that as a restart, and loses at most the changes from just before it.

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
| `sas_device_queue_depth` | `serial`, `disk` | the most commands the kernel will have out with the disk at once |
| `sas_device_timeout_seconds` | `serial`, `disk` | how long the kernel waits for an answer before giving up on a command |

- A disk goes by its `serial`. That is what joins it to other tools, such as [smartctl_exporter](https://github.com/prometheus-community/smartctl_exporter) for its SMART data.
- Nothing of that kind is repeated here. `sas_device_info` ties a serial number to a slot and a lane.
- Command errors climb slowly on a healthy disk. A tool that reads SMART data asks questions a disk may decline, and each counts.
- So watch for one disk climbing faster than its neighbours.
- Timeouts have no such noise. Any at all is worth a look.
- Fewer commands answered than sent, for long, is a command stuck with the disk.
- Sent less answered near `sas_device_queue_depth`, for long, is a disk given more than it can take.

## Enclosures

| Reading | Labels | What it says |
|---|---|---|
| `sas_enclosure_info` | `enclosure`, `vendor`, `product`, `revision`, `id`, `expander`, `vendor_specific` | always 1; one for each enclosure |
| `sas_enclosure_slots` | `enclosure` | how many slots it has |
| `sas_enclosure_slot_status` | `enclosure`, `slot`, `status` | always 1; `ok`, `not_installed` for an empty slot, or a fault |
| `sas_enclosure_slot_fault` | `enclosure`, `slot` | 1 when the slot's fault light is asked for, or a fault is sensed |
| `sas_enclosure_slot_locate` | `enclosure`, `slot` | 1 when the slot's locate light is on |
| `sas_enclosure_slot_powered` | `enclosure`, `slot` | 1 when the enclosure has power to the slot, 0 when it has turned it off |
| `sas_enclosure_element_status` | `enclosure`, `type`, `element`, `status` | always 1; each part that is not a slot. *Root* |
| `sas_enclosure_element_fault` | `enclosure`, `type`, `element` | 1 when the enclosure reports the part as failed. *Root* |
| `sas_enclosure_element_predicted_failure` | `enclosure`, `type`, `element` | 1 when the enclosure expects the part to fail. *Root* |
| `sas_enclosure_element_locate` | `enclosure`, `type`, `element` | 1 when the part's locate light is on. *Root* |
| `sas_enclosure_fan_speed_rpm` | `enclosure`, `element` | how fast a fan is turning. *Root* |
| `sas_enclosure_temperature_celsius` | `enclosure`, `element` | what a temperature sensor reads. *Root* |
| `sas_enclosure_temperature_limit_celsius` | `enclosure`, `element`, `limit` | a limit the enclosure holds that sensor to. *Root* |
| `sas_enclosure_voltage_volts` | `enclosure`, `element` | what a voltage sensor reads. *Root* |
| `sas_enclosure_voltage_limit_ratio` | `enclosure`, `element`, `limit` | a limit the enclosure holds that sensor to, as a share of what it should read. *Root* |
| `sas_enclosure_current_amperes` | `enclosure`, `element` | what a current sensor reads. *Root* |
| `sas_enclosure_current_limit_ratio` | `enclosure`, `element`, `limit` | a limit the enclosure holds that sensor to, as a share of what it should read. *Root* |
| `sas_enclosure_power_supply_problem` | `enclosure`, `element`, `problem` | 1 when a power supply is in that trouble. *Root* |
| `sas_enclosure_condition` | `enclosure`, `condition` | the enclosure's own summary of itself. *Root* |

`status` is `ok`, `not_installed`, `critical`, `non_critical`, `unrecoverable`, `unavailable`, `unknown`, `unsupported` or `no_access_allowed`.

`vendor_specific` is whatever the enclosure's maker put in the part of its description left to them. A Supermicro backplane puts its firmware version there, `x40-66.16.11.0`, as storcli shows it.

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
| `voltage_sensor` | a voltage sensor | its voltage and limits |
| `current_sensor` | a current sensor | its current and limits |
| `sas_expander`, `sas_connector`, `enclosure` and the like | the enclosure's other parts | nothing more |

- A part that is not fitted has a status and nothing measured. A stopped fan reads 0; a missing fan has no speed.
- A backplane only knows the fans and power supplies plugged into it, not the ones on the motherboard.
- A Supermicro backplane names a power supply by its serial number. A swapped supply is a new `element`.
- A locate light is read for fans, power supplies, sensors, expanders, connectors and the enclosure itself. The JBOD backplanes this was written on report both power supplies' lights as on, all the time.

### Limits

`limit` is `high_critical`, `high_warning`, `low_warning` or `low_critical`.

- An enclosure holds each temperature sensor to limits of its own. A backplane's chip may be allowed 100 degrees where the board around it is allowed 46.
- A voltage or current limit is a share of what the sensor should read, as the enclosure gives it: 0.15 is 15% above, -0.15 is 15% below. The enclosure never says what the sensor should read, so it is not guessed; the sensor's name usually says, `12VEXP` or `3.3VCC`.
- A current sensor has only high limits.
- A limit the enclosure does not set is left off.
- An enclosure that does not give limits has none on the page.

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

## Leaving parts out

Everything is on the page unless `--skip` (`SAS_EXPORTER_SKIP`) names it: `--skip phy-errors,slots`. A part left out is not asked for either, so leaving out one that costs the hardware a question saves the question too.

`--metrics` (`SAS_EXPORTER_METRICS`) names the only parts to ask about instead, from the same list, and `--skip` then leaves parts out of those: `--metrics enclosures --skip slots`. A part named brings what is within it and what it is within, so `--metrics slots` keeps the enclosures the slots are read through. What is asked of the hardware itself comes only when named: `--metrics phys` has the lanes as the kernel lists them, and how often their links changed only with `link-changes` or `card-sensors` named too. The `sas_poll_*` readings are a part like any other, so name `poll` as well to keep them.

| `--skip` | What is left out | What is not asked any more |
|---|---|---|
| `poll` | `sas_poll_*` | nothing: they are how the asking goes |
| `cards` | `sas_card_*` but `sas_card_up`, and what the card is asked itself | nothing: it is all in `/sys` |
| `expanders` | `sas_expander_*` | nothing |
| `phys` | `sas_phy_*`, with `phy-errors` and `link-changes` | as for those two, and the connector pages |
| `phy-errors` | `sas_phy_errors_total` | the error counts, each fetched from the card or an expander as it is read |
| `link-changes` | `sas_phy_link_changes_total`, `sas_expander_changes_total` | about a hundred questions of each card a round |
| `devices` | `sas_device_*` | each disk's files in `/sys` |
| `enclosures` | `sas_enclosure_*`, with `slots` and `enclosure-sensors` | as for those two |
| `slots` | the `sas_enclosure_slot*` readings | each slot, each of which has the kernel fetch the enclosure's whole status |
| `card-sensors` | all that is asked of a card itself, `link-changes` with it | everything asked through `/dev/mpt3ctl` |
| `enclosure-sensors` | all that is asked of an enclosure itself | everything asked through an enclosure's `/dev/sgN` |

- `sas_card_up` and `sas_exporter_build_info` are always on the page.
- A name that is no part, in either list, stops the program at start, naming the parts there are, rather than leave nothing out unnoticed.
- The cost of a part shows in `sas_poll_duration_seconds`: links for the cards, phys and disks, enclosures for the slots, sensors for what is asked of the hardware.

## Left out on purpose

The kernel lists a few more things that would say nothing, or say it wrongly:

- A slot's activity light. The kernel's `active` file gives back what was last written to it, never what the light is doing.
- A phy's device type, protocols and whether it is enabled. They follow from what the phy is on, which `expander` says, and a phy switched off is `disabled`.
- An expander's `level`. It reads 1 for an expander chained behind another, too.
- A card's `fw_queue_depth`. It is the limit plus the few the driver keeps for itself.
- What storcli shows of a card's memory errors, its firmware package check and its PSoC version. A trace of storcli on an HBA 9500 shows it reading pages of the card's that Broadcom has not published, IO Unit page 12, Manufacturing pages 9 and 30 and an extended page of its own, and reading from the card's flash. Without their layout there is no telling which bytes are which, and a 9500 has no memory of its own to count errors in.
- The chip revision storcli shows, `A0`. The kernel gives only the number it is made from, which says nothing more.

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
