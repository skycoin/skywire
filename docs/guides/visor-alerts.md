# Offline alerts

A hypervisor can mail you, push a notification to your phone, or post to a
webhook when a visor it manages goes
offline, and once more when the visor is back. A visor that returns within the
offline delay sends nothing.

The alerts run in the hypervisor's own visor. They watch every visor that
hypervisor manages, including ones that are offline but still remembered (see
`skywire cli visor hv forget` below). The `--watch` list adds other visors,
which are checked only by their dmsg discovery entry.

## Turning it on

On a package install, set the address with autoconfig. It writes
`ALERTEMAIL` in `/etc/skywire.conf`, so later updates keep it.

```
skywire autoconfig --alert-email you@example.com
```

`skywire cli config gen --alert-email` does the same for a config you generate
yourself. Several addresses are comma separated.

On a running visor, change the settings directly. Only the flags you give
change, the change applies at once, and it is saved to `alerts.json` in the
visor's local directory. That file wins over the config's `alerts` block and
survives a config regen.

```
skywire cli visor alerts set --email you@example.com --offline-after 15m
skywire cli visor alerts test
skywire cli visor alerts
```

`alerts test` sends a test alert to every configured address, webhook and
push device.
`alerts` with no subcommand shows the settings and the state of each watched
visor. `alerts set --email ''` stops mail alerts.

The same settings are in the hypervisor UI, in the Offline alerts section at
the top of the visor's settings page.

## The offline delay

`--offline-after` is how long a visor must stay offline before you are told.
It takes 1m to 1h and defaults to 5m. The same delay applies to a visor that
is up but has lost its link to the hypervisor, and it is also the grace given
to a visor that said it is restarting.

Do not use the 1 minute minimum in production. Short link drops are common and
would then send an alert and a recovery each time.

## Visor states

`skywire cli visor hv ls` and the hypervisor UI show one of these states for
each managed visor.

| State | Meaning |
|-------|---------|
| online | Connected and answering. |
| reconnecting | The link to the hypervisor dropped, but the visor answers the hypervisor's call back over skynet or dmsg. |
| restarting | The visor said goodbye on a clean shutdown or restart. |
| detached | The visor removed this hypervisor with `hv rm`. It is not watched and never alerts. |
| offline | No answer. |

A reconnecting or restarting visor is held: its offline delay does not run.
A reconnecting visor that stays so for the delay sends a link-lost alert. A
restarting visor that has not come back within the delay becomes offline,
and its offline delay starts then.

## What gets sent

**Offline.** A visor has been offline for the delay.

**Link lost.** The visor is up and answers the call back, but has not
reconnected to its hypervisor for the delay.

**Recovery.** The visor is back. When the board's uptime is known the alert
says whether it rebooted during the outage (likely power loss or a crash) or
stayed up (a network or internet outage).

**Site outage.** When every watched visor behind one public IP goes down
together, one site alert replaces the per-board alerts, and one recovery
follows.

**Storage warning.** Sent while a board is still online when its root
filesystem turns read-only, the kernel logs storage I/O errors, or disk use
passes `--disk-warn` (default 90 percent). These are the early signs of a
failing SD card.

**IP conflict.** Two or more recently online visors behind one public IP
report the same LAN IP.

Alerts carry a diagnosis line. For a single board that goes offline, an online
visor on the same LAN checks whether the board still answers there. The answer tells a board that is up with skywire down from one that
is powered off, hung or unplugged.

## Delivery

Without a relay, mail goes straight to each recipient domain's MX on port 25.
Many home connections block port 25, and large mail providers often refuse
mail from home addresses. In that case use an SMTP relay.

```
skywire cli visor alerts set --smtp-host smtp.example.com --smtp-starttls --smtp-user me@example.com --smtp-pass '...'
```

`--smtp-port` defaults to 587 with `--smtp-starttls` and 25 otherwise. Port
465 is implicit TLS. `--no-smtp` removes the relay. `--from` sets the sender,
which defaults to `skywire-alerts@<hostname>` or the relay login.

`--webhook <url>` posts a JSON body for each alert, as well as or instead of
mail. It has the event, the visor's key, hostname and addresses, the times,
the diagnosis and a one line `text`.

## Push notifications

Push needs no mail setup. Open the hypervisor UI on the phone or computer that
should get the alerts, go to the Offline alerts section of the hypervisor's own
visor settings, press **Notify this device** and allow notifications. Alerts
then arrive as system notifications, even with the page closed. Press **Send
test** to try it.

- **iPhone and iPad** need iOS 16.4 or later. Open the UI in Safari, tap Share,
  then Add to Home Screen, open it from the Home Screen icon and press the
  button there. Push does not work in a normal Safari tab.
- **Android and desktop** browsers subscribe directly. Brave needs "Use Google
  services for push messaging" turned on in its privacy settings.
- The page must be a secure context. `http://localhost` is one, so a UI
  reached through an SSH tunnel works. A LAN address over plain http is not,
  and the section says so.

`skywire cli visor alerts` lists the subscribed devices and the visor's push
key. Remove one with `alerts set --push-remove <id>`, or all of them with
`--push-remove all`. A device whose subscription the push service has dropped
is removed on the next alert. A browser visor cannot watch, but its page can
subscribe to the alerts of a native hypervisor it manages.

## Forgetting a visor

The hypervisor remembers every visor it has managed, in `known-visors.json`
beside its `users.db`, and keeps watching it while it is offline. Remove a
visor you have retired so it stops alerting.

```
skywire cli visor hv forget <public-key>
```

A connected visor cannot be forgotten.
