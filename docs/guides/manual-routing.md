# Manual Routing (Advanced)

_Note: In most cases, routes are automatically created when applications
connect. Manual routing is an advanced feature for specific use cases._

## Route Types

1. **App/Consume Rule** — terminates at the local visor for app consumption.
2. **Forward Rule** — forwards packets to the next hop in a multi-hop route.
3. **Intermediary Forward Rule** — forwards without app-level routing info.

## Creating a Simple Direct Route

First, list existing routes to find an available route ID:

```bash
skywire cli visor route ls
```

Create an app/consume rule from your visor to a remote visor:

```bash
# Get your local public key
LOCAL_PK=$(skywire cli visor pk)

# Get destination visor public key (from service discovery)
REMOTE_PK="02..." # VPN server, proxy server, or any visor

# Create consume rule
skywire cli visor route add a \
  -i 1 \              # route ID (increment for each route)
  -l $LOCAL_PK \      # local public key
  -m 43 \             # local port (app-specific: 43=VPN client, 13=proxy client)
  -p $REMOTE_PK \     # remote public key
  -q 44               # remote port (44=VPN server, 3=proxy server)
```

## Port Numbers by Application

Common app port mappings:
- **1** — skychat
- **3** — skysocks (proxy server)
- **13** — skysocks-client (proxy client)
- **43** — vpn-client
- **44** — vpn-server

View app ports:
```bash
skywire cli visor app ls
```

## Making Apps Use Your Manual Route

**Important**: Apps will automatically use manually created routes if the
route's local/remote PK and ports match the app's connection requirements.

Example — VPN client using manual route:
```bash
# 1. Create route with local port 43, remote port 44
skywire cli visor route add a -i 1 -l $LOCAL_PK -m 43 -p $VPN_SERVER_PK -q 44

# 2. Start VPN client - it will automatically use route ID 1
skywire cli vpn start $VPN_SERVER_PK
```

The VPN client connects from port 43 → server port 44, matching the route rule, so it's used automatically.

## Creating Multi-Hop Routes

Multi-hop routes require coordination between visors. This example creates a 2-hop route: A → B → C.

**On Visor A (source):**
```bash
# Create forward rule to visor B
skywire cli visor route add c \
  -i 1 \
  -j 2 \              # next route ID (on visor B)
  -k $TRANSPORT_ID \  # transport ID to visor B
  -l $VISOR_A_PK \
  -m 43 \
  -p $VISOR_C_PK \    # final destination
  -q 44
```

**On Visor B (intermediary):**
```bash
# Create intermediary forward rule to visor C
skywire cli visor route add b \
  -i 2 \
  -j 3 \              # next route ID (on visor C)
  -k $TRANSPORT_ID    # transport ID to visor C
```

**On Visor C (destination):**
```bash
# Create consume rule
skywire cli visor route add a \
  -i 3 \
  -l $VISOR_C_PK \
  -m 44 \
  -p $VISOR_A_PK \
  -q 43
```

## Finding Transport IDs

Get transport IDs for multi-hop routing:
```bash
# List all transports
skywire cli visor tp ls

# Find specific transport by remote public key
skywire cli visor tp ls | grep $REMOTE_PK
```

## Route Finder (Automatic Multi-Hop Discovery)

Query the route finder to discover available multi-hop paths:

```bash
# Find routes between two visors
skywire cli visor route find $SOURCE_PK $DEST_PK

# With custom hop limits
skywire cli visor route find $SOURCE_PK $DEST_PK --min 2 --max 3

# Find routes from local visor to destination (auto-detects local PK)
skywire cli visor route find $DEST_PK
```

The route finder returns the optimal path, which you can then configure manually using the commands above.

## Managing Routes

List all routing rules:
```bash
skywire cli visor route ls
```

View specific route details:
```bash
skywire cli visor route ls -i <route-id>
```

Remove a routing rule:
```bash
skywire cli visor route rm <route-id>
```

## Troubleshooting

**Routes not being used:**
- Verify local/remote ports match app requirements (`skywire cli visor app ls`)
- Check transports exist to the next hop (`skywire cli visor tp ls`)
- Ensure route IDs are sequential and unique
- Verify public keys are correct

**Multi-hop routes failing:**
- Each intermediary must have the correct transport ID to the next hop
- Route IDs must chain correctly across visors
- Keep-alive duration may need adjustment for long routes (`--keep-alive 60s`)
