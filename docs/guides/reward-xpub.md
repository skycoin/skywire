# Set an account xpub as the reward address

A visor's reward address is the account xpub of a Skycoin BIP44 wallet. The
reward system derives a fresh address from it for every payout and never pays
the same address twice. This page shows how to make the wallet, which xpub to
copy, and how to set it.

From mid-November 2026 rewards are planned to require an xpub. A single
Skycoin address can already no longer be set, and one set earlier is still
paid for now.

## Why an xpub

When coins are spent from an address, its public key goes on the blockchain.
An address that is paid again after that keeps receiving coins behind a public
key anyone can see, which a future quantum computer could attack. A single
reward address also shows everyone every reward a visor earned.

With an xpub each daily payout goes to a new address of your wallet. No
address is reused, and the payouts are not all on one address.

## Which xpub

A BIP44 wallet shows three xpubs for each account. Only the account xpub is
right.

| xpub | BIP44 path | depth | use it |
|---|---|---|---|
| Account xPub | `m/44'/8000'/0'` | 3 | yes |
| External chain xPub | `m/44'/8000'/0'/0` | 4 | no |
| Change chain xPub | `m/44'/8000'/0'/1` | 4 | no |

All three start with `xpub`, so check the depth before you set one.
`skywire skycoin cli verifyXpub <xpub>` prints it and needs no node.

```
$ skywire skycoin cli verifyXpub <xpub>
Valid xpub key (depth=3, child=2147483648)    <- account xpub, correct
Valid xpub key (depth=4, child=0)             <- chain xpub, wrong
```

A chain xpub is refused. The payout addresses derived from it are not
addresses your wallet shows, so you would not see those rewards arrive.

## Make the wallet on the Wallet page

The Wallet page of the hypervisor UI makes the wallet in your browser. This
needs a visor newer than v1.3.104.

1. Open `http://127.0.0.1:8000/#/wallet`. Choose **New** and give the wallet
   a name. **Wallet type** is already **BIP44 (HD Wallet)**. Write the seed
   down on paper and type it into **Confirm seed**. Set a **Password**, which
   encrypts the seed in this browser. Leave **Seed passphrase** empty unless
   you will keep it with the seed, because the wallet cannot be restored
   without it. Press **Create**.

   ![create a BIP44 wallet, seed hidden](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/dashboard-01-create.png)

2. Open the wallet in the list and type the seed once more to confirm you
   wrote it down. The wallet shows **Account 0** with its external and change
   chains.

   ![the new wallet](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/dashboard-02-wallet.png)

3. Press **Show xPub Keys** and copy the key under
   **Account xPub (m/44'/coin'/0') derives both chains**. The five addresses
   under it are the first ones rewards go to.

   ![the account xpub](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/dashboard-03-xpub.png)

The wallet lives in this browser's storage. The xpub and the addresses show
without the password, and sending asks for it. Each time the page opens it
looks on the blockchain for addresses of the account that were paid, up to
20 unused ones past the last used one, so rewards sent to addresses it never
showed appear with their balance. In another browser choose **Load**, type
the seed, pick **BIP44 (HD Wallet)** and the same seed passphrase.

## Make the wallet in the web wallet

This is the same wallet kept in a wallet directory instead of the browser.

1. Start the web wallet with a wallet directory of its own, then open
   `http://127.0.0.1:8001` in a browser.

   ```
   skywire skycoin web --wallet-dir ~/skycoin-reward-wallet
   ```

   The page exists only while this command runs, so keep the terminal open
   until you have copied the xpub. It is not the hypervisor UI, which is on
   port 8000.

2. Choose **New**, give the wallet a name, and set **Wallet type** to
   **BIP44 (HD Wallet)**. Write the seed down on paper and type it into
   **Confirm seed**, then press **Create**.

   ![create a BIP44 wallet, seed hidden](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/01-create-bip44.png)

3. Open the wallet in the list. If it shows a plain address list without
   **Show xPub Keys**, reload the page. Press **Encrypt Wallet** and set a
   password. Do this even if you typed a password on the create form, because
   the first wallet form does not encrypt the wallet in the current release.

   ![Encrypt Wallet and Show xPub Keys](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/02-encrypt-wallet.png)

   ![the password dialog](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/03-encrypt-password.png)

4. Press **Show xPub Keys** on the account. Copy the key under
   **Account xPub (m/44'/coin'/0') derives both chains** (green below). Do not
   copy the **xPub** under **External Chain** (red below).

   ![the account xpub and the external chain xpub](https://github.com/skycoin/skywire/raw/develop/docs/img/reward-xpub/04-account-xpub.png)

## Make the wallet from the command line

The skycoin CLI talks to a running wallet API. The web wallet serves one, and
`--enable-seed-api` lets the CLI read the seed it needs to export the key.

```
skywire skycoin web --wallet-dir ~/skycoin-reward-wallet --enable-seed-api
```

In a second terminal point the CLI at it.

```
export RPC_ADDR=http://127.0.0.1:8001
skywire skycoin cli walletCreate -t bip44 -m -l reward
```

`walletCreate` encrypts the wallet by default and asks for the password. It
prints the wallet, whose file name is in `meta.filename`, for example
`2026_10_30_e2cf.wlt`. It does not print the seed. Show it once and write it
down.

```
skywire skycoin cli showSeed 2026_10_30_e2cf.wlt
```

Export the account xpub with `--path=0`. The default path `0/0` gives the
external chain xpub, which is the wrong one.

```
skywire skycoin cli walletKeyExport 2026_10_30_e2cf.wlt -k xpub --path=0
skywire skycoin cli verifyXpub <the xpub it printed>
```

`verifyXpub` must say `depth=3`. Leave out `-p` on these commands so the
password is prompted for and does not end up in your shell history. Stop the
web wallet when you are done, or restart it without `--enable-seed-api`.

## Set it on the visor

From the command line, on the machine running the visor:

```
skywire cli reward <account-xpub>
```

Check what is set with no argument.

```
skywire cli reward
```

It prints the xpub and the first five addresses derived from it. Rewards go to
these in order, each day to the next one not used yet, so they should be the
first addresses your wallet lists. If they are not, the wrong xpub is set.

In the hypervisor UI, open the visor, find **Rewards** and press
**Set address** or **Change address**. To set it on several visors at once,
use **Change rewards address** in the visor list menu. Both fields take the
account xpub. The Rewards tab lists the same five addresses under the xpub, and
under the field while you type one in. Leaving the field empty removes the
reward address.

On a package install also put it in `/etc/skywire.conf` as `REWARDSKYADDR`, so
that a config regeneration keeps it. The
[mainnet rules](https://github.com/skycoin/skywire/blob/develop/rewards/mainnet_rules.md#Skycoin-Address)
show how.

## Common mistakes

- Copying the External Chain xPub, or exporting with the default `--path=0/0`.
  Its depth is 4. Use the Account xPub, depth 3.
- Setting a plain Skycoin address. It is refused with
  `a single skycoin address is no longer accepted as a reward address`.
- Sharing the seed, an xprv or a private key. The reward address only ever
  needs the xpub. Nobody asks for the seed to pay rewards.

An xpub can spend nothing, but it reveals every address of that account and
so its whole balance and history. Give it only to the reward system, and use
a wallet account that is just for rewards.
