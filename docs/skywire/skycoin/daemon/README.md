# skywire skycoin daemon

[← skywire skycoin](../README.md)

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌
└─┐├┴┐└┬┘│  │ │││││
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘
 skycoin wallet

Environment variables:
  FIBER_TOML             Path to a fiber.toml file to load custom fibercoin configuration.
                         Sets default values before CLI flags are applied; flags override.
  GENESIS                Path to a genesis wallet JSON file (address, pubkey, seckey).
                         Takes precedence over fiber.toml genesis values.
  USER_BURN_FACTOR       Coinhour burn factor for user-created transactions.
  USER_MAX_TXN_SIZE      Maximum transaction size in bytes for user-created transactions.
  USER_MAX_DECIMALS      Maximum decimal places for droplet precision (max 6).
```

## Usage

```
skywire skycoin daemon [flags]
```

## Flags

```
      --address string                              IP Address to run application on. Leave empty to default to a public interface
      --bip44-coin uint32                           BIP44 coin type (default 8000)
      --block-publisher                             run the daemon as a block publisher
      --blockchain-public-key string                public key of the blockchain (default "0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a")
      --blockchain-secret-key string                secret key of the blockchain
      --burn-factor-create-block uint               coinhour burn factor applied when creating blocks (default 10)
      --burn-factor-unconfirmed uint                coinhour burn factor applied to unconfirmed transactions (default 10)
      --coin-hours-name string                      display name for coin hours (default "Coin Hours")
      --coin-hours-name-singular string             singular display name for coin hours (default "Coin Hour")
      --coin-hours-ticker string                    ticker symbol for coin hours (default "SCH")
      --coin-name string                            name of the coin (default "skycoin")
      --color-log                                   Add terminal colors to log output (default true)
      --connection-rate duration                    How often to make an outgoing connection (default 5s)
      --custom-peers-file string                    load custom peers from a newline separate list of ip:port in a file. Note that this is different from the peers.json file in the data directory
      --data-dir string                             directory to store app data (defaults to $HOME/.skycoin) (default "$HOME/.skycoin")
      --db-path string                              path of database file
      --db-read-only                                open bolt db in read only mode
      --disable-api-sets string                     disable API set. Options are READ, STATUS, WALLET, TXN, NET_CTRL, INSECURE_WALLET_SEED, STORAGE, EXPLORER. Multiple values should be separated by comma
      --disable-csp                                 disable Content Security Policy in http response
      --disable-csrf                                disable CSRF check
      --disable-default-peers                       disable the hardcoded default peers
      --disable-header-check                        disables the host, origin and referer header checks.
      --disable-incoming                            Don't allow incoming connections
      --disable-networking                          Disable all network activity
      --disable-outgoing                            Don't make outgoing connections
      --disable-pex                                 disable PEX peer discovery
      --display-name string                         display name of the coin (default "Skycoin")
      --download-peerlist                           download a peers.txt from the peerlist URL (default true)
      --enable-all-api-sets                         enable all API sets except deprecated or insecure. Applied before the disable API sets flag.
      --enable-api-sets string                      enable API set. Options are READ, STATUS, WALLET, TXN, NET_CTRL, INSECURE_WALLET_SEED, STORAGE, EXPLORER. Multiple values should be separated by comma (default "READ,TXN")
      --enable-gui                                  Enable GUI
      --explorer-url string                         URL of the block explorer (default "https://explorer.skycoin.com")
      --genesis-address string                      genesis address (default "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6")
      --genesis-signature string                    genesis block signature (default "eb10468d10054d15f2b6f8946cd46797779aa20a7617ceb4be884189f219bc9a164e56a5b9f7bec392a804ff3740210348d73db77a37adb542a8e08d429ac92700")
      --genesis-timestamp uint                      genesis block timestamp (default 1426562704)
      --gui-dir string                              static content directory for the HTML interface
      --host-whitelist string                       Hostnames to whitelist in the Host header check. Only applies when the web interface is bound to localhost.
      --http-prof                                   run the HTTP profiling interface
      --http-prof-host string                       hostname to bind the HTTP profiling interface to (default "localhost:6060")
      --launch-browser                              launch system default webbrowser at client startup
      --legacy-peer-compat                          Allow connections from legacy peers that don't send blockchain pubkey
      --localhost-only                              Run on localhost and only connect to localhost peers
      --log-level string                            Choices are: debug, info, warn, error, fatal, panic (default "INFO")
      --logtofile                                   log to file
      --max-block-size uint                         maximum total size of transactions in a block (default 32768)
      --max-connections int                         Maximum number of total connections allowed (default 128)
      --max-decimals-create-block uint              max number of decimal places applied when creating blocks (default 3)
      --max-decimals-unconfirmed uint               max number of decimal places applied to unconfirmed transactions (default 3)
      --max-default-peer-outgoing-connections int   The maximum default peer outgoing connections allowed (default 2)
      --max-in-msg-len int                          Maximum length of incoming wire messages (default 1048576)
      --max-incoming-connections int                Maximum number of incoming connections allowed (default 120)
      --max-last-blocks-count uint                  Maximum number of blocks to response for API /api/v1/last_blocks (default 256)
      --max-out-msg-len int                         Maximum length of outgoing wire messages (default 262144)
      --max-outgoing-connections int                Maximum number of outgoing connections allowed (default 8)
      --max-txn-size-create-block uint              maximum size of a transaction applied when creating blocks (default 32768)
      --max-txn-size-unconfirmed uint               maximum size of an unconfirmed transaction (default 32768)
      --no-ping-log                                 disable "reply to ping" and "received pong" debug log messages
      --peerlist-size int                           Max number of peers to track in peerlist (default 65535)
      --peerlist-url string                         URL to download peers.txt from (requires peerlist download enabled) (default "https://downloads.skycoin.com/blockchain/peers.txt")
      --port int                                    Port to run application on (default 6000)
      --profile-cpu                                 enable cpu profiling
      --profile-cpu-file string                     where to write the cpu profile file (default "cpu.prof")
      --qr-uri-prefix string                        prefix for QR code URIs (default "skycoin")
      --reset-corrupt-db                            reset the database if corrupted, and continue running instead of exiting
      --storage-dir string                          location of the storage data files
      --ticker string                               coin ticker symbol (e.g., SKY) (default "SKY")
      --user-agent-remark string                    additional remark to include in the user agent sent over the wire protocol
      --verify-db                                   check the database for corruption
      --version                                     show node version
      --version-url string                          URL for version checking (default "https://version.skycoin.com/skycoin/version.txt")
      --wallet-crypto-type string                   wallet encryption type (see default for format options)
                                                     (default "scrypt-chacha20poly1305")
      --wallet-dir string                           location of the wallet files
      --web-interface                               enable the web interface (default true)
      --web-interface-addr string                   addr to serve web interface on (default "127.0.0.1")
      --web-interface-cert string                   skycoind.cert file for web interface HTTPS. If not provided, will autogenerate or use skycoind.cert in data dir
      --web-interface-https                         enable HTTPS for web interface
      --web-interface-key string                    skycoind.key file for web interface HTTPS. If not provided, will autogenerate or use skycoind.key in data dir
      --web-interface-password string               password for the web interface
      --web-interface-plaintext-auth                allow web interface auth without https
      --web-interface-port int                      port to serve web interface on (default 6420)
      --web-interface-username string               username for the web interface
```

## Global Flags

```
  -h, --help   show help menu
```

---
_Generated by `skywire doc` — do not edit by hand._
