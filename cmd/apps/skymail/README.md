# skymail

`skymail` is the visor's mailbox. It takes SMTP on port 25 over dmsg and
skynet for `<anything>@<base32-pk>.skynet` (or `.dmsg`) and keeps the mail
in a Maildir. The transport authenticates every sender's PK, so it needs
no domain, certificate, SPF or DKIM.

Every visor runs it unless the config's `skymail` section or
`skywire cli mail settings enable=false` turns it off. The visor starts it
in process with these arguments, taken from the config section:

```
skywire app skymail --dir <mail dir> [--max-message-size N] [--max-total-size N] [--max-age D]
```

To run it as a separate process, list it in the launcher config with
`"launcher_mode": "external"` and give it `--addr 127.0.0.1:<port>`. The
visor's mail API (`skywire cli mail`, the hypervisor Mail tab, the phone
app) then reaches its control surface on that address, with the token the
app writes to `control-token` beside the mail.

The visor keeps its secret key. The app asks it over the app RPC to seal
and open envelopes, and to name the visors it manages and its
hypervisors, which are the default helpers and relay-for lists. See
[docs/guides/skymail-bridge.md](../../../docs/guides/skymail-bridge.md)
for using it.
