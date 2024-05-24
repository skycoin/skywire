# Skycoin Reward System

Skycoin rewards are the primary incentive for participation in the skywire network.

This document details the administration of the reward system and distribution of rewards for the skywire network.

User-facing details of this system can be found in the [mainnet rules article](mainnet_rules.md)

This system replaces the [skywire whitelisting interface](https://whitelist.skycoin.com), and enables the daily distribution of rewards.

### User Participation

Eligible skywire visors based on the criteria outlined in the [mainnet rules](mainnet_rules.md) may receive rewards when the user sets a reward address; either from the hypervisor UI or from the CLI with:

```
skywire-cli reward <skycoin address>
```

**NOTE: in order for the setting to persist updates for package-based linux installations, it is recommended to set the reward address in /etc/skywire.conf and run `skywire-autoconfig` to update the setting**

### System Survey `node-info.json`

The reward address is set in a text file called reward.txt inside the local folder specified in the visor's config.

It is possible to view the survey that would be generated with

```
skywire cli survey
```

**Setting the reward address will generate a system survey** inside the local folder called node-info.json.

It should be noted that the system survey generation requires root for many of its fields, but no essential field currently requires root


### Log & Survey Collection

The log collection and [reward processing](#reward-processing) happens hourly via [skywire-reward.service](/scripts/rewards/services/skywire-reward.service) - triggered to run hourly by [skywire-reward.timer](/scripts/rewards/services/skywire-reward.timer).

The log collection run can be viewed here:
https://fiber.skywire.dev/log-collection

The surveys and transport logs are collected with

```
skywire cli log -s <secret-key-of-survey-whitelisted-public-key>
```

The surveys are only permitted to be collected by `survey_whitelist` keys which are specified in the visor's config.

These `survey_whitelist` keys are specified by the [conf service](https://conf.skywwire.skycoin.com) and are fetched / included in the visor's config when the config is generated.

The collected surveys are then checked and backed up.

The following scripts are used by the reward system:

[`getlogs.sh`](/scripts/rewards/getlogs.sh) - a wrapper script for survey and transport bandwidth log collection via `skywire cli log`
[`reward.sh`](/scripts/rewards/reward.sh) - a wrapper script for reward calculation via `skywire cli rewards`
[`gettps.sh`](/scripts/rewards/gettps.sh) - a wrapper script for collecting responses to transport setup-node requests via `skywire svc tps ls`
[`testproxies.sh`](/scripts/rewards/testproxies.sh) - WIP - a wrapper script for testing curl response time over the skywire socks5 proxy (not used for reward calculation)

### Reward Processing

The rewards are calculated by `skywire cli rewards calc` with the aid of [`reward.sh`](scripts/rewards/reward.sh) to produce the reward distribution data for the previous day's uptime.

### Per-IP reward limit

The total share of rewards for any given ip address is limited to 8, or one share per visor which met uptime and other requirements.

If there are more than 8 visors which meet uptime and other requirements,the reward shares are divided among those skycoin addresses set in the survey for the reward eligible visors at that ip address

### MAC Address reward limit

To avoid a user running multiple instances of skywire on virtual machines, the MAC addresses from the surveys are compared to the mac addresses in all other surveys. If any two visors list the same mac address for the first interface after `lo` these are considered the same machine and 1 reward share is divided evenly between all the visors which list the same MAC address.

## Automation via systemd service

Automation of the hourly log & survey collection is accomplished via systemd service and timer

/etc/systemd/system/[`skywire-reward.service`](/scripts/rewards/services/skywire-reward.service)

**Note: change the user and working directory in the above systemd service**

This service is called by a timer which triggers it to run hourly

/etc/systemd/system/[`skywire-reward.timer`](/scripts/rewards/services/skywire-reward.timer).


## fiber.skywire.dev

The 'frontend' of the reward system, is currently running at [fiber.skywire.dev](https://fiber.skywire.dev) and is reliant upon on the output of certain cli commands ~~and some scripts~~

[`skywire cli rewards ui`](cmd/skywire-cli/commands/rewards/ui.go) serves the reward system frontend or user interface - via http and dmsghttp.

The service which runs the reward system UI:
/etc/systemd/system/[`fiberreward.service`](/scripts/rewards/services/fiberreward.service)

A wrapper script [`getlogs.sh`](scripts/rewards/getlogs.sh) is used to redirect the output of `skywire cli log` to a file, which is displayed at:

https://fiber.skywire.dev/log-collection

The above endpoint will live-update via streamed html / chunked transfer encoding when the hourly survey and log collection run is ongoing

Here shows links to the reward calculations and distribution data by day:

https://fiber.skywire.dev/skycoin-rewards

on each linked page, the distribution data is displayed with a link to the explorer for that transaction if it was broadcast. Also displayed are the public keys and their reward shares, or the reason why they were not rewarded

The frontend may be run either with flags or by using a conf file such as the following:
fr.conf
```
WEBPORT=80
#DMSGPORT=80
REWARDPKS=('02114054bc4678537e1d07b459ca334a7515315676136dcedeb1fe99eadc4a52bf' '02b390f82db10067b05828b847ddbbf267c7bfb66046e2eb9b01ad81e097da7675' '026ccef9d8c87259579bc002ce1ffcf27ddd0ab1b6c08b39ad842b25e9da321c4f' '026ccef9d8c87259579bc002ce1ffcf27ddd0ab1b6c08b39ad842b25e9da321c4f' '03362e132beb963260cc4ccc5e13b611c30b6f143d53dc3fbcd1fed2247873dcc6')
DMSGHTTP_SK=<reward-system-secret-key>

```

`REWARDPKS` are public keys permitted to access the non public data generated & collected by the reward system; these keys are permitted as well to `POST` the raw transaction to be distributed.



### Reward distribution transaction

The reward system UI is served over dmsghttp. Keys which are whitelisted by the reward system are able to view the collected system surveys and other non-public reward system data. Additionally, these whitelisted keys are permitted to `POST` a signed raw transaction to the reward system, which will be broadcast by the reward system. In this way, it is possible to avoid having funds on the same machine as is running the reward system.

This is accomplished by the following script:

```
#!/usr/bin/bash
source sendrewards.conf
skycoin-cli status ; [[ $? -ne 0 ]] && echo 'Skycoin wallet not running ; exiting' && exit 1
skywire dmsg curl $REWARD_SYS_URL/reward -d "$(skycoin-cli createRawTransaction $WALLET_FILE -a $FROM_ADDRESS --csv <(skywire dmsg curl  $REWARD_SYS_URL/$(skywire dmsg curl $REWARD_SYS_URL/skycoin-rewards/csv -s $REWARD_WL_SK) -s $REWARD_WL_SK | tr -d ' ' | awk -F, '{printf "%s,%.3f\n", $1, int($2*1000)/1000}' |  grep -v '^,0.000$'))" -s $REWARD_WL_SK
```

The script sources a `.conf` file of the following format

```
WALLET_FILE="$HOME/.skycoin/wallets/<wallet-filename>.wlt"
FROM_ADDRESS="<skycoin-address-containing-funds-to-distribute>"
REWARD_WL_SK=<secret-key-of-whitelisted-public-key>
REWARD_SYS_URL="dmsg://<reward-system-public-key>:80"
```

before the script is run and the transaction is attempted to be broadcast, it's crucial to check that the hourly [log collection and reward calculation](https://fiber.skywire.dev/log-collection) is not ongoing.

### Reward Notifications

When the transaction is broadcast by the reward system, it's transaction ID is recorded by appending a file which is monitored by the reward telegram bot. The telegram bot will then generate a notification in https://t.me/skywire_reward when a change to that file is detected. **Note: this will eventually be supplemented with or replaced by a notification via skychat.**
