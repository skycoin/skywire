# Skycoin node and wallet

The skywire binary carries a full Skycoin node, its wallet, a command line
client and a thin client web wallet. Nothing else needs to be installed.

## Running a node

```
skywire skycoin daemon
```

This syncs the blockchain into the data directory, `$HOME/.skycoin` by
default (`--data-dir` changes it). The node's API listens on
`http://127.0.0.1:6420` (`--web-interface-addr` and `--web-interface-port`).

## The wallet GUI

There are two ways to serve the wallet GUI from the node.

```
skywire skycoin daemon --enable-gui
```

`--enable-gui` keeps the wallets in the node, in `$HOME/.skycoin/wallets`
(`--wallet-dir` changes it), and serves the GUI at `http://127.0.0.1:6420`.

```
skywire skycoin daemon --web-wallet
```

`--web-wallet` serves the GUI without the node's wallet API, and the wallets
are kept in the browser instead.

Add `--launch-browser` to open the GUI in the default browser at startup.

On v1.3.107, `--enable-gui` alone gives a 404 because the GUI needs the WALLET
API set, which is off by default. From v1.3.108 `--enable-gui` turns it on
itself. On v1.3.107 add the API sets by hand:

```
skywire skycoin daemon --enable-gui --enable-api-sets READ,STATUS,TXN,WALLET
```

or use `--enable-all-api-sets`.

## The command line client

```
skywire skycoin cli --help
```

The client talks to the node named by `RPC_ADDR`, which defaults to
`http://127.0.0.1:6420`. It can query any node, including a public one.

`addressSpent` says whether an address has ever sent coins. It prints, per
address, `spent`, `received_outputs`, `spent_outputs` and `balance`.

```
RPC_ADDR=https://node.skycoin.com skywire skycoin cli addressSpent <address>
```

This matters for rewards. From the reward day of November 15, 2026 a single
reward address that has sent coins is not paid. An xpub reward address is not
affected. See [reward-xpub.md](reward-xpub.md).

## Fiber coins

A fiber coin is a blockchain built from the Skycoin code with its own
parameters. Point the node at its config with `FIBER_TOML`.

```
FIBER_TOML=mycoin.toml skywire skycoin daemon
```

The file name must end in its format extension, normally `.toml`. Keys the
file leaves out fall back to the Skycoin defaults. In the `[node]` section,
`display_name` is the name shown in the wallet and `coin_name` sets the coin
name the API reports. When the file sets no data
directory, the node uses `$HOME/.<name>`, from `coin_name` or else
`display_name`, lowercased. Flags given on the command line override the file.

## A web wallet for several coins

`skywire skycoin web` is a thin client wallet. It keeps no blockchain and asks
nodes over their API. Give `-n` once per node to use several coins in one
wallet.

```
skywire skycoin web -n https://node.skycoin.com -n https://node.example.com
```

It serves on `http://127.0.0.1:8001` by default (`--host`, `--port`). The
node defaults to `https://node.skycoin.com`. See `skywire skycoin web --help`
for reaching nodes over the skywire mesh.

## A public node as a service

The Linux packages ship `skywire-skycoin.service`. It runs
`skywire skycoin daemon` with its data in `/var/lib/skywire-skycoin` and reads
extra flags from `SKYCOIN_ARGS` in `/etc/default/skywire-skycoin`.

```
SKYCOIN_ARGS="--enable-api-sets READ,TXN --web-interface-addr 0.0.0.0 --disable-header-check"
```

The API listens on 127.0.0.1 unless `--web-interface-addr` says otherwise.
`--disable-header-check` lets clients reach it by a public host name.

Then enable it.

```
sudo systemctl enable --now skywire-skycoin
```
