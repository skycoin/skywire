# Running a Public (Reachable) Visor

A **public visor** is one that other visors can open transports *to* from the
internet. It advertises its transport endpoints in the address resolver, so any
peer — including browser (wasm) visors — can dial it directly, and it can serve
as a route hop / relay. Most visors don't need to be public (they reach the
network fine as clients over dmsg), but public visors are what the network routes
*through*, and they're the ones a browser visor can form a **direct** transport to.

Reachability comes down to a few concrete factors. Get these four right and the
visor is reachable by every transport type; miss one — most commonly the **UDP**
firewall rule — and some or all direct transports silently fail.

## The four factors

1. **`is_public: true`** — opt in to being advertised as a public visor. Set it
   in the config (`"is_public": true`) or toggle **Public** in the hypervisor UI.
   The visor then registers with service discovery and, once it receives an
   inbound STCPR connection, is marked validated. If it receives **no** external
   STCPR within `PublicVisorRegistrationTimeout` (**10 minutes**) it is
   deregistered — i.e. "public" is only honoured once reachability is proven.

2. **A stable `transport_port`** — set it so every transport type shares one
   known port instead of random per-type ports you can't forward:

   ```bash
   skywire cli config gen --transport-port 7773 [other flags]
   ```

   With `transport_port` set, `stcpr`+`swsr` (WS) share that **TCP** port and
   `squicr`+`sudph`+`swtr` (WT) share that **UDP** port (see the table below).
   Left at `0`, each type binds a *random* port that no router rule can target.

3. **Router port-forward — TCP *and* UDP** — forward `transport_port` to the
   visor's LAN IP for **both** protocols. On most consumer routers this is a
   single rule with protocol set to *Both*:

   ```
   WAN :7773  →  <visor-LAN-IP>:7773   proto: TCP+UDP
   ```

4. **Host firewall — TCP *and* UDP** — open the same port on the machine itself.
   This is the step most often half-done: TCP gets opened, UDP is forgotten, and
   then `stcpr` works while `squicr`/`sudph`/`swtr` all fail. Open both:

   ```bash
   # firewalld
   sudo firewall-cmd --permanent --add-port=7773/tcp --add-port=7773/udp
   sudo firewall-cmd --reload

   # ufw
   sudo ufw allow 7773/tcp
   sudo ufw allow 7773/udp

   # nftables (in your input chain)
   tcp dport 7773 accept
   udp dport 7773 accept
   ```

## Transport type → protocol

Forward and open `transport_port` for **both** protocols — the direct transport
types split across them:

| Type              | Protocol | Notes                                             |
|-------------------|----------|---------------------------------------------------|
| `stcpr`           | TCP      | shares the port via a TCP cmux                    |
| `swsr` (WS)       | TCP      | WebSocket, shares the stcpr TCP port              |
| `squicr` (QUIC)   | UDP      | shares the port via a QUIC mux                    |
| `sudph`           | UDP      | can STUN hole-punch, so may connect *without* a forward |
| `swtr` (WT)       | UDP      | WebTransport — **the carrier browsers dial**      |

Because `sudph` hole-punches, it can succeed even when UDP isn't forwarded — so
don't treat a working `sudph` transport as proof the port is open. Test `squicr`
or `swtr` for that (below).

> Browser (wasm) visors can only open **WT** (UDP) — and WS on an insecure page —
> so **UDP reachability is what makes your visor reachable by browsers.**

## Verify reachability

From **another** visor, dial each type to this visor's public key. Each should
report `Established`:

```bash
skywire cli tp add -t stcpr  <this-visor-pk>
skywire cli tp add -t squicr <this-visor-pk>
skywire cli tp add -t swtr   <this-visor-pk>
skywire cli tp rm <transport-id>   # clean up afterwards
```

Also confirm from the visor's own side:

- Its log shows `Public visor validated: received external STCPR connection`.
- `skywire cli visor hv ls` lists it (from a connected hypervisor).

## Troubleshooting

- **`stcpr` works but `squicr`/`swtr` fail with `timeout: no recent network
  activity`** — TCP is reaching the visor but UDP isn't. The port serves fine on
  `127.0.0.1` but not from outside. Fix the **UDP** side: router forward for UDP,
  and the host firewall (`--add-port=<port>/udp`). This is the single most common
  cause of "my visor won't accept WebTransport / browser connections".
- **All types fail** — the port isn't forwarded at all, the forward points at the
  wrong LAN IP, or the visor isn't listening on `transport_port` (check
  `ss -ltn`/`ss -lun` for the port). Confirm `transport_port` is set and the visor
  restarted after the change.
- **Visor drops out of service discovery after ~10 minutes** — it never received
  an external STCPR, so its public status was never validated. That's the same
  reachability problem as above, seen from the discovery side.
- **Behind CGNAT / no control of the edge router** — you can't port-forward, so
  inbound direct transports won't work. The visor still participates fully as a
  client over dmsg; it just won't be a public hop. `sudph` (hole-punched) may
  still form.
