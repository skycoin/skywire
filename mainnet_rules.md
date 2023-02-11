![skywire logo](https://user-images.githubusercontent.com/26845312/32426764-3495e3d8-c282-11e7-8fe8-8e60e90cb906.png)

# Skywire Reward Eligibility Rules

* We are transitioning to new system for rewards, deprecating the skywire whitelist
* The rules in this article may change at any time, depending on if there are problems
* We will attempt to address any issues reported via [@skywire](https://t.me/skywire) Telegram channel

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

Reward notifications will happen via the skychat app, which is included with the skywire release (PENDING IMPLEMENTATION)


## Rules & Requirements

* Up to 8 (eight) visors may receive rewards per location (ip address)
* 75% uptime is required to be eligible to receive rewards
* A valid skycoin address must be set for the visor
* The visor must be running on approved [hardware](#hardware)
* the visor responds to intermittent pings (i.e. it's possible to establish transports to that visor)


## Rewards

**The new reward system requires Skywire v1.3.4**

Requirement established 2-11-2023

Rewards Cutoff date for updating 4-1-2023

The required minimal Skywire version will be incremented periodically.

In the event of changes to the survey decryption key, it will be required to update in order to change the reward address

The rewards in the new system will be **paid daily or weekly**

The skycoin address to be rewarded can be set from the cli:

```
skywire-cli reward <skycoin-address>
```

![image](https://user-images.githubusercontent.com/36607567/213941582-f57213b8-2acd-4c9a-a2c0-9089a8c2604e.png)


or via the hypervisor UI.

![image](https://user-images.githubusercontent.com/36607567/213941478-34c81493-9c3c-40c2-ac22-e33e3683a16f.png)

the example above shows the genesis address for the skycoin blockchain. **Please do not use the genesis address.**

### How it works

The skycoin reward address is set per the visor via the cli or the hypervisor, in a text file contained in the "local" folder (local_path in the skywire config file). This address is written into the [system survey](https://github.com/skycoin/skywire/tree/develop/cmd/skywire-cli#survey) and served, along with transport logs, via dmsghttp.

This survey will be fetched on a daily basis with [`dmsgget`](https://github.com/skycoin/dmsg/tree/develop/cmd/dmsgget), along with the transport logs, and checked to verify hardware and other requirements, etc. The transport logs from both ends of any given transport are compared and verified.

The system survey is encrypted to the public key of the package maintainer, this key is present in the skywire github repository and is included with any future release

### Reward tiers

There are three tiers for rewards.

* **TIER 3** The lowest tier is distributed to all nodes which meet the basic requirements.

Other tiers are based on bandwidth which was handled by the visor. Meaning the logs from each end of the transport were fetched and agree

* **TIER 2** If the visor processed **any** verifiable bandwidth, the visor will have earned the rewards of the second tier plus those of the lowest tier.
* **TIER 1** If the visor processed above the average amount of bandwidth, it will receive first tier rewards, in addition to the lowest tier.

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
