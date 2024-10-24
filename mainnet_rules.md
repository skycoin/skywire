![skywire logo](https://user-images.githubusercontent.com/26845312/32426764-3495e3d8-c282-11e7-8fe8-8e60e90cb906.png)

# Skywire Reward Eligibility Rules

**We have transitioned to a new system with daily reward distribution**


* The rules in this article may change at any time, depending on if there are problems
* We will attempt to address any issues reported via [@skywire](https://t.me/skywire) Telegram channel
* Reward corrections are not possible after the rewards for a day have been distributed.

The required minimum Skywire version will be incremented periodically.

## Introduction

<div align="center"><em> Updates to this article will be followed by a notification via the <a href="https://t.me/SkywirePSA">official Skywire PSA channel</a> on Telegram.</em>
</div>
<br>

All information about rewards will be published here. Please ask for clarification in the [@skywire](https://t.me/skywire) Telegram channel if some things appear to not be covered.

Please join [@SkywirePSA](https://t.me/SkywirePSA) for public service announcements (PSA) regarding the skywire network, update notices, changes to this article, etc.

Reward distribution notifications are on telegram [@skywire_reward](https://t.me/skywire_reward).

Information about reward distribution as well as other metrics for the skywire network can be found at [fiber.skywire.dev](https://fiber.skywire.dev)

# Uptime Reward Pools

816000 Skycoin are distributed annually to those visors which meet the mimimum uptime and the other requirements listed below, in two equally sized reward pools.

The reward amount for a day is evenly divided among those eligible participants for a given reward pool on the basis of having met uptime and other requirements, for the previous day.

A total of up to ~1117.808 Skycoin __per pool__ are distributed daily in non leap-years.

A total of up to ~1114.754  Skycoin __per pool__ are distributed daily in leap-years.

The two reward pools are differentiated by architecture ; one pool for ARM / RISC / MIPS architectures, the other pool for AMD64 / x86_64 / i686 architecture machines. The requirements are otherwise identical for reward eligibility in these pools.

## Rules & Requirements

To receive Skycoin rewards for running skywire, the following requirements must be met:


* 1) **Minimum skywire version v1.3.26** - Cutoff October 1st 2024

* 2) **75% [uptime](#uptime) per day** minimum is required to be eligible to receive rewards

* 3) ~The visor must be an **ARM or RISC architecture SBC** running on approved [hardware](#hardware)~

* 4) Visors must be running on **[the skywire production deployment](#deployment)** with a config that is updated on every version. No default keys or addresses of this configuration may be removed - but you can add keys where applicable.

* 5) **Only 1 (one) visor per machine**

* 6) **Up to 8 (eight) visors may each receive 1 (one) reward share per location (ip address)**

* 7) **A valid [skycoin address](#skycoin-address)** must be set for the visor

* 8) The visor must be **[connected to the DMSG network](#connection-to-dmsg-network)**

* 9) **[Transports](#transportability) can be established to the visor**

* 10) **The visor responds to [Transport Setup-Node requests](#transport-setup-node)**

* 11) **The visor responds to [pings](#ping-latency-metric)** - needed for latency-based rewards

* 12) **The visor produces [transport bandwidth logs](#transport-bandwidth-logs)** - needed for bandwidth-based rewards

* 13) **The visor produces a [survey](#survey)** when queried over dmsg by any keys in the survey_whitelist array by default


### Exceptions for Deployment Changes with dmsghttp-config (Chinese users)

All the production deployment services may be accessed by the visor over the dmsg network when the visor runs with a dmsghttp config.

This type of config is generated automatically based on region via:
```
skywire cli config gen -b --bestproto
```
to circumvent ISP blocking of http requests.

In order to bootstrap the visor's to connection to the dmsg network (via TCP connection to an individual dmsg server) the [dmsghttp-config.json](/dmsghttp-config.json) is provided with the skywire binary release.

In the instance that the skywire production deployment changes - specifically the dmsg servers:
* it will be necessary to update to the next version or package release which fixes the dmsg servers.
OR
* it will be necessary to manually update the [dmsghttp-config.json](/dmsghttp-config.json) which is provided by your skywire installation.

Currently, **there is no mechanism for updating the dmsghttp-config.json which does not require an http request** ; a request which may be blocked depending on region.

In this instance, the visor will not connect to any service because it is not connected to the dmsg network, so it will not be possible for the visor to accumulate uptime or for the reward system to collect the survey, which are prerequisites for reward eligibility.

As a consequence of this; any visors running a dmsghttp-config, and hence any visors running in regions such as China, the minimum version requirement for obtaining rewards is not only the latest available version,
but __the latest release of the package__ unless the dmsghttp-config.json is updated manually within your installation.

## Verifying Requirements & Eligibility

### Version

View the version of skywire you are running with:
```
skywire cli -v
skywire visor -v
```

The update deadlines specify the version of software required as of (i.e. on or before) the specified date in order to maintain reward eligibility:


**Reward eligibility after 9-1-2024 requires Skywire v1.3.25**

Requirement established 8-21-2024

Rewards Cutoff date for updating 9-1-2024

**Reward eligibility after 10-1-2024 requires Skywire v1.3.26**

Requirement established 9-24-2024

Rewards Cutoff date for updating 10-1-2024

### Deployment

The deployment your visor is running on can be verified by comparing the services configured in the visor's .json config against [conf.skywire.skycoin.com](https://conf.skywire.skycoin.com)

```
cat /opt/skywire/skywire.json
```

The service configuration will be automatically updated any time a config is generated or regenerated.

For those visors in china or those running a dmsghttp-config, compare the dmsghttp-config of your current installation with the dmsghttp-config on the develop branch of [github.com/skycoin/skywire](https://github.com/skycoin/skywire)

The same data in a different format should be displayed in the [dmsg-discovery all_servers](https://dmsgd.skywire.skycoin.com/dmsg-discovery/all_servers) page. Ensure that the dmsghttp-config.json in your installation has the same ip addresses and ports for the dmsg server keys.

The data from the dmsg discovery should be considered authoritative or current.

### Uptime

Daily uptime statistics for all visors may be accessed via the

- [uptime tracker](https://ut.skywire.skycoin.com/uptimes?v=v2)

or using skywire cli

```
skywire cli ut -n0 -k <public-key>
```

For example with a locally running visor:
```
skywire cli ut -n0 -k $(skywire cli visor pk)
```


### Skycoin Address

The skycoin address to be rewarded can be set from the cli:

```
skywire cli reward <skycoin-address>
```

![image](https://user-images.githubusercontent.com/36607567/213941582-f57213b8-2acd-4c9a-a2c0-9089a8c2604e.png)


```
$ skywire cli reward --help

	reward address setting

	Sets the skycoin reward address for the visor.
	The config is written to the root of the default local directory

	this config is served via dmsghttp along with transport logs
	and the system hardware survey for automating reward distribution

Flags:
      --all   show all flags

```

```
$ skywire cli reward 2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6
Reward address:
  2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6
```

```
$ skywire cli reward --help

    skycoin reward address set to:
    2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6

Flags:
      --all   show all flags
```

or via the hypervisor UI.

![image](https://user-images.githubusercontent.com/36607567/213941478-34c81493-9c3c-40c2-ac22-e33e3683a16f.png)

the example above shows the genesis address for the skycoin blockchain. **Please do not use the genesis address.**

It is __highly recommended__ to set the reward address in the file '/etc/skywire.conf' by adding this line to the file:

```
echo "REWARDSKYADDR=('2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6')" | sudo tee -a /etc/skywire.conf
```

**PLEASE DO NOT USE THE GENESIS ADDRESS!**


Add your skycoin address there and run:
```
skywire-autoconfig
```

on linux (assumes you have installed the package)

If this file does not exist for you, it can be created with
```
skywire cli config gen -q | sudo tee /etc/skywire.conf
```

**If you do this, YOU MUST UNCOMMENT THE FOLLOWING LINES OF THE FILE:**
```
PKGENV=true
BESTPROTO=TRUE
```

you can open the file in an editor to make that change, for instance nano

```
sudo nano /etc/skywire.conf
```

### Connection to DMSG network

For any given visor, the system hardware survey, transport setup-node survey, and transport bandwidth logs are collected **hourly** by the reward system over dmsg.

This can be verified by examining the visor's logging:

![image](https://github.com/skycoin/skywire/assets/36607567/eb66bca1-fc9e-4c80-a38a-e00a73f675d0)

```
[DMSGHTTP] 2024/10/09 - 22:31:45 | 200 |        47.2µs |                 | 03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3:51405 | GET      /health
[DMSGHTTP] 2024/10/09 - 22:31:46 | 200 |     193.325µs |                 | 03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3:51457 | GET      /node-info
[DMSGHTTP] 2024/10/09 - 22:31:47 | 200 |       98.93µs |                 | 03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3:51503 | GET      /2024-10-10.csv
```

The collected surveys and transport bandwidth log files should be visible in the survey index here:

[fiber.skywire.dev/log-collection/tree](https://fiber.skywire.dev/log-collection/tree)

An example of one such entry:
```
├─┬025e3e4e324a3ac2771e32b798ca3d8859e585ac36938b15a31d20982de6aa31fc
│ ├──2024-05-02.csv
│ ├──2024-05-03.csv
│ ├──2024-05-04.csv
│ ├──2024-05-05.csv
│ ├──2024-05-06.csv
│ ├──2024-05-07.csv
│ ├──health.json     Age: 13m5s {"build_info":{"version":"v1.3.21","commit":"5131943","date":"2024-04-13T15:03:26Z"},"started_at":"2024-05-07T08:52:09.895919222Z"}
│ ├──node-info.json          "v1.3.21"
│ └──tp.json         Age: 6m56s []
```

Note: the transport bandwidth logging CSV files will only exist if it was generated; i.e. if there were transports to that visor which handled traffic.

Note: the system survey (node-info.json) will only exist if the reward address is set.

If your visor is not generating such logging or errors are indicated, please reach out to us on telegram [@skywire](https://t.me/skywire) for assistance

### Transportability

It is not required that a visor run any service, such as a vpn or socks5 proxy server, which permits direct access to the internet from your ip address.
However, it is required that the visor is able to act as a hop along a route.
A module is active at runtime which checks that transports may be established to that visor - the visor creates a dmsg transport to itself every few minutes to ensure transportability.
If it's not possible to create a dmsg transport to the same visor after three attempts,the visor will shut down automatically.
**It is expected that the visor will be restarted by a process control mechanism if the visor shuts down for any reason.** In the officially supported linux packages, systemd will restart the visor if it stops; regardless of the exit status of the process.

### Transport setup node

Previously, the transport setup node was run continuously as part of the reward system to ensure that visors were responding as expected to transport setup-node requests.
However, there were intermittent issues with reliability of the results ; because there is no caching mechanism for responsiveness to transport setup-node requests as there exists for uptime.

Currently, the transport setup-nodes which are configured for the visor are included in the survey and verified as an eligibility requirement for rewards by the reward system.

### Ping Latency metric

Not yet implemented

### Transport bandwidth logs

The visor will only produce transport bandwidth logs in response to transports being established to them. These are collected, along with the system survey, and are displayed on the reward system [here](https://fiber.skywire.dev/log-collection/tplogs)

In the future, it is anticipated that the transport bandwidth logs and ping metric will be collected by the transport discovery automatically.

### Survey

On setting the skycoin reward address, the visor will generate and serve a system survey over dmsg.

Only keys which are whitelisted in the survey_whitelist array of the visor's config will have access to collect the survey.

To print the survey (approximately) as it would be served, one can use:
```
skywire cli survey -p
```

**The purpose of the survey is strictly for checking [eligibility requirements](#rules--requirements) numbers 3 through 7.**

**Setting a skycoin address is considered as consent for collecting the survey ; the survey is not generated unless you set a skycoin address**

We respect your privacy.

### Verifying other requirements

If the visor is not able to meet the [eligibility requirements](#rules--requirements) numbers 8 through 13, that is usually not the fault of the user - nor is it something the user is expected to troubleshoot on their own at this time. Please ask for assistance on telegram [@skywire](https://t.me/skywire)

## Reward System overview

The skycoin reward address may be set for each visor using `skywire cli` or for all visors connected to a hypervisor from the hypervisor UI

The skycoin reward address is in a text file contained in the "local" folder (local_path in the skywire config file) i.e `local/reward.txt`.

The skycoin reward address is also included with the [system hardware survey](https://github.com/skycoin/skywire/tree/develop/cmd/skywire/README.md#survey) and served, along with transport logs, via dmsghttp.

The system survey ('local/node-info.json') is fetched hourly by the reward system via
```
skywire cli log
```
along with transport bandwidth logs.

The index of the collected files may be viewed at [fiber.skywire.dev/log-collection/tree](https://fiber.skywire.dev/log-collection/tree)

Once collected from the nodes, the surveys for those visors which met uptime are checked to verify hardware and other requirements, etc.

The system survey is only made available to those keys which are whitelisted for survey collection, but is additionally available to any `hypervisor` or `dmsgpty_whitelist` keys set in the config for a given visor.

**Setting a skycoin address is considered as consent for collecting the survey.**

The public keys which require to be whitelisted in order to collect the surveys, for the purpose of reward eligibility verification, should populate in the visor's config automatically when the config is generated.

## Reward System Funding & Distributions

The reward system is funded on a monthly basis. **Sometimes there are unexpected or unavoidable delays in the funding.** In these instances, rewards will be distributed based on the data generated when the system is funded.
In some instances, it's necessary to discard previous reward data and do multiple distributions to handle backlog of reward system funds.

**We do our best to ensure fair reward distribution** - but the system itself is not infinitely flexible.
If there is no good way to rectify historical or undistributed rewards backlog, it will be distributed going forward as multiple distributions on the same day to those current active participants in the network.

## Deployment Outages

While we do our best to maintain the skywire production deployment, there have been instances of issues or outages in the past. We attempt to correct these outages as soon as possible and avoid recurrant disruptions.

The policy for handling rewards in the instance of a deployment outage is to repeat the distribution for the last day where uptime was unaffected by the outage; for the duration of the outage.

## Hardware

As of November 2024, skywire rewards are open to all computer hardware and architectures.

If there is not a release for your desired architecture, we can attempt to add it, on request.
