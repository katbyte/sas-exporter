# Queries

## Scraping

Prometheus:

```yaml
scrape_configs:
  - job_name: sas
    scrape_interval: 30s
    static_configs:
      - targets: ['127.0.0.1:9088']
```

Grafana Alloy:

```alloy
prometheus.scrape "sas" {
  targets         = [{"__address__" = "127.0.0.1:9088"}]
  job_name        = "sas"
  scrape_interval = "30s"
  forward_to      = [prometheus.remote_write.default.receiver]
}
```

## Cards

```promql
sas_card_up == 0                                              # a card is gone or being reset
increase(sas_card_resets_total[1d]) > 0                       # a card's firmware was restarted
sas_card_pcie_link_width < sas_card_pcie_maximum_link_width    # a card in a narrower slot than it was made for, or not seated
sas_card_temperature_celsius                                  # how hot each card is running
sas_card_speed_ratio < 1                                      # a card that has slowed itself down
sas_card_commands_in_flight / sas_card_commands_in_flight_limit > 0.8  # a card near the most it will take
```

## Lanes

```promql
increase(sas_phy_errors_total[1h]) > 0                       # a lane counted errors in the last hour
sas_phy_link_state{state=~"failed|disabled"} == 1            # a link that would not come up
sas_phy_link_rate_gigabits_per_second                        # link speeds: one lane of a cable slower than the rest came up slow
count by (card, expander) (sas_phy_info{attached="upstream"})  # lanes each backplane has back to the card: fewer than before is a lane lost
```

Which backplane connector, and so which cable, each lane with errors is in:

```promql
increase(sas_phy_errors_total[1d]) * on (card, expander, phy) group_left (connector) sas_phy_connector_info > 0
```

The errors on the cables alone, leaving out the lanes to disks:

```promql
increase(sas_phy_errors_total[1d]) * on (card, expander, phy) group_left () sas_phy_info{attached=~"upstream|expander"}
```

## Disks

```promql
sas_device_state{state!="running"} == 1                      # a disk the kernel has stopped sending to
increase(sas_device_command_timeouts_total[1h]) > 0          # a disk that did not answer
sas_device_commands_total - sas_device_commands_completed_total >= sas_device_queue_depth  # a disk with as much as it will take
sas_enclosure_slot_powered == 0                              # a slot the enclosure has turned off
```

A disk's serial number and slot beside the errors on its link:

```promql
increase(sas_phy_errors_total[1d]) * on (card, expander, phy) group_left (serial, disk, enclosure, slot) sas_device_info
```

## Enclosures

```promql
sas_enclosure_slot_fault == 1                                # a slot with its fault light on
sas_enclosure_slot_status{status!~"ok|not_installed"} == 1   # a slot its enclosure is unhappy with
sas_enclosure_element_status{status!~"ok|not_installed"} == 1  # a fan, power supply or sensor its enclosure is unhappy with
sas_enclosure_power_supply_problem == 1                      # a power supply in trouble, and which trouble
sas_enclosure_element_predicted_failure == 1                 # a part its enclosure expects to fail
sas_enclosure_fan_speed_rpm == 0                             # a fan that has stopped
changes(sas_enclosure_condition[1h]) > 0                     # an enclosure that has changed its mind about how it is doing
```

A sensor past the limit its enclosure holds it to:

```promql
sas_enclosure_temperature_celsius > ignoring (limit) sas_enclosure_temperature_limit_celsius{limit="high_warning"}
```

A voltage limit is a share of what the sensor should read, which its name says. The 12 volt rail past its warning:

```promql
sas_enclosure_voltage_volts{element="12VEXP"} > ignoring (limit) 12 * (1 + sas_enclosure_voltage_limit_ratio{element="12VEXP", limit="high_warning"})
```

## The program itself

```promql
time() - sas_poll_last_success_timestamp_seconds > 900       # readings gone stale
increase(sas_poll_failures_total[1h]) > 0                    # polls that failed
```
