![skywire logo](https://user-images.githubusercontent.com/26845312/32426764-3495e3d8-c282-11e7-8fe8-8e60e90cb906.png)

# Skywire Reward Eligibility Rules

Notice: the [skywire whitelist](https://whitelist.skycoin.com) is deprecated since April 1st 2023.

We have transitioned to a new system with daily reward distribution

* The rules in this article may change at any time, depending on if there are problems
* We will attempt to address any issues reported via [@skywire](https://t.me/skywire) Telegram channel

The required minimum Skywire version will be incremented periodically.

#### Table of Contents
* [Introduction](#introduction)
* [Rules & Requirements](#rules--requirements)
* [Rewards](#rewards)
  * [How it works](#how-it-works)
  * [Reward Tiers](#reward-tiers)
* [Hardware](#hardware)

## Introduction

<div align="center"><em> Updates to this article will be followed by a notification via the <a href="https://t.me/SkywirePSA">official Skywire PSA channel</a> on Telegram.</em>
</div>
<br>

All information about rewards will be published here. Please ask for clarification in the [@skywire](https://t.me/skywire) Telegram channel if some things appear to not be covered. Join [@SkywirePSA](https://t.me/SkywirePSA) for public service announcements (PSA) regarding the skywire network.

Reward distribution notifications are on telegram [@skywire_reward](https://t.me/skywire_reward).

Information about reward distribution as well as other metrics for the skywire network can be found at [fiber.skywire.dev](https://fiber.skywire.dev)

# Uptime Reward Pool

408000 Skycoin are distributed annually to those visors which meet the mimimum uptime and the other requirements listed below.

A total of up to ~1117.808 Skycoin are distributed daily in non leap-years; evenly divided among those eligible participants on the basis of having met uptime for the previous day.

## Rules & Requirements

To obtain Skycoin rewards for running skywire, the following requirements must be met.
The update deadlines specify the version of software required as of (i.e. on or before) the specified date in order to maintain reward eligibility:

* **Minimum skywire version v1.3.13**

* **Minimum skywire version v1.3.14** - Cutoff January 1st 2024

* **Minimum skywire version v1.3.15** - Cutoff January 14th 2024

* The visor must be an **ARM architecture SBC running on approved [hardware](#hardware)**

* Visors must be running on **[the skywire production deployment](https://conf.skywire.skycoin.com)**

* **Up to 8 (eight) visors may each receive 1 reward share per location (ip address)**

* **75% uptime per day** is required to be eligible to receive rewards

* **A valid skycoin address** must be set for the visor

* The visor must be **connected to the DMSG network**

* **Transports can be established to the visor**

* **The visor responds to pings** - needed for latency-based rewards

* **The visor produces transport bandwidth logs** - needed for bandwidth-based rewards

### Exceptions for Deployment Changes with dmsghttp-config (Chinese users)

All the production deployment services may be accessed by the visor over the dmsg network when the visor runs with a dmsghttp config.

This type of config is generated automatically based on region (via `skywire-cli config gen -b --bestproto`), to circumvent ISP blocking of http requests.

In order to bootstrap the visor's to connection to the dmsg network (via TCP connection to an individual dmsg server) the `dmsghttp-config.json` is provided with the skywire binary release.

In the instance that the skywire production deployment changes - specifically the dmsg servers - it will be necessary to update to the next version or package release which fixes the dmsg servers - or else to manually update the `dmsghttp-config.json` which is provided by your skywire installation.

Currently, **there is no mechanism for updating the dmsghttp-config.json which does not require an http request** ; a request which may be blocked depending on region.

In this instance, the visor will not connect to any service because it is not connected to the dmsg network, so it will not be possible for the visor to accumulate uptime or for the reward system to collect the survey, which are prerequisite for reward eligibility.

As a consequence of this; any visors running a dmsghttp-config, and hence any visors running in regions such as China, the minimum version requirement for obtaining rewards is not only the latest available version, but __the latest release of the package__ unless the dmsghttp-config.json is updated manually.

## Verifying Requirements & Eligibility

### Version

View the version of skywire you are running with:
```
skywire-cli -v
skywire-visor -v
```

**Reward eligibility before 1-1-2024 requires minimum Skywire version v1.3.13**

**Reward eligibility after 1-1-2024 requires Skywire v1.3.14**

Requirement established 12-10-2023

Rewards Cutoff date for updating 1-1-2024

**Reward eligibility after 1-14-2024 requires Skywire v1.3.15**

Requirement established 12-18-2023

Rewards Cutoff date for updating 1-14-2024

### Deployment

The deployment your visor is running on can be verified by comparing the services configured in the visor's `.json` config against [conf.skywire.skycoin.com](https://conf.skywire.skycoin.com)

The service configuration will be automatically updated any time a config is generated or regenerated.

For those visors in china or those running a dmsghttp-config, compare the dmsghttp-config of your current installation with the dmsghttp-config on the develop branch of [github.com/skycoin/skywire](https://github.com/skycoin/skywire)


### Uptime

Daily uptime statistics for all visors may be accessed via the
- [uptime tracker](https://ut.skywire.skycoin.com/uptimes?v=v2)
or using skywire-cli
- `skywire-cli ut -n0 -k <public-key>`

### Skycoin Address

The skycoin address to be rewarded can be set from the cli:

```
skywire-cli reward <skycoin-address>
```

![image](https://user-images.githubusercontent.com/36607567/213941582-f57213b8-2acd-4c9a-a2c0-9089a8c2604e.png)


or via the hypervisor UI.

![image](https://user-images.githubusercontent.com/36607567/213941478-34c81493-9c3c-40c2-ac22-e33e3683a16f.png)

the example above shows the genesis address for the skycoin blockchain. **Please do not use the genesis address.**

### Connection to DMSG network - Survey & transport log collection

For any given visor, the system survey and transport bandwidth logs should be downloaded **hourly** over dmsghttp.

This can be verified by examinimg the visor's logging:

![image](https://github.com/skycoin/skywire/assets/36607567/eb66bca1-fc9e-4c80-a38a-e00a73f675d0)

Note: the transport bandwidth logs will only exist if it was generated; i.e. if there were transports to that visor which handled traffic.

Note: the system survey (node-info.json) will only exist if the reward address is set.

If your visor is not generating such logging, please reach out to us on telegram [@skywire](https://t.me/skywire) for assistance

### Verifying other requirements

If the visor is not able to meet the other requirements, that is usually not the fault of the user nor is it something the user is expected to troubleshoot at this time. Please ask for assistance on telegram [@skywire](https://t.me/skywire)

## Reward System overview

The skycoin reward address may be set for each visor using skywire-cli or for all visors connected to a hypervisor from the hypervisor UI

The skycoin reward address is in a text file contained in the "local" folder (local_path in the skywire config file) i.e `local/reward.txt`.

The skycoin reward address is also included with the [system survey](https://github.com/skycoin/skywire/tree/develop/cmd/skywire-cli#survey) and served, along with transport logs, via dmsghttp.

The system survey (`local/node-info.json`) is fetched hourly by the reward system via `skywire-cli log`; along with transport bandwidth logs.

The index of the collected files may be viewed at [fiber.skywire.dev/log-collection/tree](https://fiber.skywire.dev/log-collection/tree)



Once collected from the nodes, the surveys for those visors which met uptime are checked to verify hardware and other requirements, etc.

The system survey is only made available to those keys which are whitelisted for survey collection, but is additionally available to any `hypervisor` or `dmsgpty_whitelist` keys set in the config for a given visor.

The public keys which require to be whitelisted in order to collect the surveys, for the purpose of reward eligibility verification, should populate in the visor's config automatically when the config is generated with visors of at least version 1.3.8.

## Hardware

**VM's, servers or personal computers are not permitted to collect rewards**

The following hardware is eligible for rewards:

#### Orange Pi
     - Prime
     - 2G-IOT
     - 4G-IOT
     - i96
     - Lite
     - Lite2
     - One
     - One-Plus
     - PC
     - PC-Plus
     - PC2
     - Plus
     - Plus2
     - Plus2E
     - RK3399
     - Win
     - Win-Plus
     - Zero
     - Zero LTS
     - Zero-Plus
     - Zero-Plus2
     - 3

#### Raspberry Pi
     - 1-Model-A+
     - 1-Model-B+
     - 2-Model-B
     - 3-Model-B
     - 3-Model-B+
     - 4-Model-B
     - Compute Module 3
     - Compute Module 4
     - Zero-W
     - Zero

#### Asus
     - Tinkerboard

#### Banana Pi
     - BPI-D1
     - BPI-G1
     - BPI-M1
     - BPI-M1+
     - BPI-M2
     - BPI-M2+
     - BPI-M2-Berry
     - BPI-M2M
     - BPI-M2U
     - BPI-M64
     - BPI-R2
     - BPI-R3
     - BPI-Zero

#### Beelink
     - X2

#### Cubieboard
     - Cubietruck
     - Cubietruck-Plus
     - 1
     - 2
     - 4

#### Geniatech
     - Developer Board IV

#### Helios
     - 4

#### Libre Computer
     - Le-Potato-AML-S905X-CC
     - Renegade-ROC-RK3328-CC
     - Tritium-ALL-H3-CC

#### MQMaker
     - MiQi

#### NanoPi
     - NanoPi
     - 2
     - 2-Fire
     - A64
     - K2
     - M1
     - M1-plus
     - M2
     - M2A
     - M3
     - M4
     - NEO
     - NEO-Air
     - NEO-Core
     - NEO-Core2
     - NEO2
     - NEO2-Black
     - S2
     - Smart4418

#### Odroid
     - C2
     - C4
     - HC1
     - HC2
     - MC1
     - XU4

#### Olimex
     - Lime1
     - Lime2
     - Lime2-eMMC
     - LimeA33
     - Micro

#### Pine
     - Pine-A64
     - Pinebook-A64
     - Sopine-A64
     - Rock64
     - ROCKPro64

#### ROCKPI
     - Rockpi 4
     - Rockpi S
     - Rockpi E
     - Rockpi N10

#### SolidRun
     - CuBox-i
     - CuBox-Pulse
     - Humming-Board
     - Humming-Board-Pulse
     - ClearCloud-8K
     - ClearFog-A38
     - ClearFog-GT-8K

#### Udoo
     - Blu
     - Bricks
     - Dual
     - Neo
     - Quad
     - X86

#### X96 Android TV Box
     - X96 mini

#### Dolamee
     - A95X F1 Smart TV Box

#### Radxa
     - ROCK Pi S

#### ZTE
     - ZXV10 B860H

**If you would like to use other boards please contact the team first for approval ; only the boards on the list are guaranteed to be eligible for rewards.**
