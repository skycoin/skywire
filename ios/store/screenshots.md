# Screenshots

The app is iPhone only (`TARGETED_DEVICE_FAMILY = 1`), so App Store Connect
needs iPhone screenshots alone. It takes one set at the largest size it
lists (6.9-inch: the Pro Max Simulators) and scales that set down for the
smaller iPhones; check the pixel sizes its upload page accepts at the time.

## Making them

```sh
make ios-core
ios/scripts/store-screenshots.sh "iPhone 18 Pro Max"            # en, zh-Hans, es
ios/scripts/store-screenshots.sh "iPhone 18 Pro Max" en         # one language
```

The script runs the screen tour (`SkywireUITests/ScreenTour`) once per
language with the status bar App Store screenshots use (9:41, full signal
and battery) and exports every step into
`ios/build/store-screenshots/<device>/<language>/`. Nothing is committed: the
pictures are rebuilt from the tour whenever the screens change.

## Which ones

Up to ten per language. From the tour's steps, in this order:

1. `2-home-connected`: Home, connected, the node card open.
2. `3-chat`: the SkyChat list.
3. `3a-apps`: the Apps hub.
4. `3b-socks`: SkySOCKS with the proxy list.
5. the wallet's home (`WalletTour`, after a restore of a test phrase).
6. `3d-fleet`: Fleet.
7. Settings.

Leave out anything that is not on a device in the build being submitted:
the SkyVPN and SkyDNS screens while they say "arrives with the build that
runs on an iPhone", and the debug-only diagnostics card. A conversation for
the store needs a second node and made-up content; never a real user's.
