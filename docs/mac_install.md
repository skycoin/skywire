# MacOS

## Installation

### Developer

Prequisites:

- Xcode
- MacOS Host

Run `make mac-installer` on the root of the `skywire` directory

It should output two installer for `amd64 (Intel Mac)` and `arm64 (M1 Macs)` so install one that depends on your CPU
arch / ISA.

### End User

- Download the pkg / dmg for your machine, depends on your CPU arch / ISA.
- Install it.

## Running

### Directory Layout

- Skywire's config directory is in `/Users/${USER}/Library/Application\ Support/Skywire`
- Runtime logs is in `/Users/${USER}/Library/Logs/Skywire/visor.log`

## Updating

Same as Installation, choose the `Update` option.